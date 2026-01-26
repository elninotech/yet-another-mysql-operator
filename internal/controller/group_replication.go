package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	databasev1alpha1 "github.com/elninotech/mysql-operator/api/v1alpha1"
	groupreplication "github.com/elninotech/mysql-operator/internal/controller/mysql/groupreplication"
	"github.com/elninotech/mysql-operator/internal/controller/util/constants"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"database/sql"
)

const clusterDomain = "cluster.local"

// grClient captures the subset of the MySQL group replication client we rely on.
// Making it an interface lets tests swap in fakes without talking to a real MySQL instance.
type grClient interface {
	Close() error
	SelfOnline(context.Context) (bool, error)
	GTIDSets(context.Context) (groupreplication.GTIDSets, error)
	EnsureReplicationUser(context.Context, string) error
	AnyOnlineMember(context.Context) (bool, error)
	Bootstrap(context.Context, string) error
	Join(context.Context, string) error
}

// newGRClient is a factory indirection so tests can inject a fake client.
var newGRClient = func(ctx context.Context, dsn string) (grClient, error) {
	return groupreplication.New(ctx, dsn)
}

func (r *MySQLReconciler) reconcileGroupReplication(ctx context.Context, mysql *databasev1alpha1.MySQL, names resourceNames) error {
	rootPass, replPass, pods, err := r.loadSecretsAndPods(ctx, mysql, names)
	if err != nil {
		return err
	}

	bootstrapped := mysql.Status.GroupReplicationBootstrapped
	restartCandidate := mysql.Status.RestartCandidate

	anyOnlineOverall, infos, cmpDB, err := r.collectGroupInfo(ctx, pods, mysql, names, rootPass, bootstrapped, restartCandidate)
	if err != nil {
		return err
	}
	defer func() {
		if cmpDB != nil {
			_ = cmpDB.Close()
		}
	}()

	if bootstrapped && !anyOnlineOverall && restartCandidate == "" && len(infos) > 0 {
		candidate, err := groupreplication.PickRestartCandidate(ctx, cmpDB, infos)
		if err != nil {
			return fmt.Errorf("pick restart candidate: %w", err)
		}
		mysql.Status.RestartCandidate = candidate
		if err := r.Status().Update(ctx, mysql); err != nil {
			logf.FromContext(ctx).Error(err, "status update failed")
		}
		return nil // status update will trigger another reconcile
	}

	return r.reconcilePods(ctx, pods, mysql, names, rootPass, replPass, bootstrapped, restartCandidate)
}

// loadSecretsAndPods fetches secrets and pods once.
func (r *MySQLReconciler) loadSecretsAndPods(
	ctx context.Context,
	mysql *databasev1alpha1.MySQL,
	names resourceNames,
) (rootPass, replPass string, pods corev1.PodList, err error) {
	rootPass, err = r.secretValue(ctx, mysql.Namespace, names.secret, constants.RootUserEnv)
	if err != nil {
		err = fmt.Errorf("load root password: %w", err)
		return
	}
	replPass, err = r.secretValue(ctx, mysql.Namespace, names.replSec, constants.ReplicationUserEnv)
	if err != nil {
		err = fmt.Errorf("load replication password: %w", err)
		return
	}
	if err = r.List(ctx, &pods,
		client.InNamespace(mysql.Namespace),
		client.MatchingLabels{"app": mysql.Name},
	); err != nil {
		err = fmt.Errorf("list pods: %w", err)
		return
	}
	return
}

func (r *MySQLReconciler) collectGroupInfo(
	ctx context.Context,
	pods corev1.PodList,
	mysql *databasev1alpha1.MySQL,
	names resourceNames,
	rootPass string,
	bootstrapped bool,
	restartCandidate string,
) (bool, []groupreplication.GTIDInfo, *sql.DB, error) {
	var (
		anyOnline bool
		infos     []groupreplication.GTIDInfo
		cmpDB     *sql.DB
	)

	for _, pod := range pods.Items {
		if !podReady(&pod) || pod.Status.PodIP == "" {
			continue
		}

		podOrdinal, dsn := buildDSN(pod, names, mysql, rootPass)

		grClient, err := newGRClient(ctx, dsn)
		if err != nil {
			return anyOnline, infos, cmpDB, fmt.Errorf("connect %s: %w", pod.Name, err)
		}

		online, err := grClient.SelfOnline(ctx)
		if err != nil {
			_ = grClient.Close()
			return anyOnline, infos, cmpDB, fmt.Errorf("self online %s: %w", pod.Name, err)
		}
		if online {
			anyOnline = true
			_ = grClient.Close()
			continue
		}

		if bootstrapped && restartCandidate == "" {
			sets, err := grClient.GTIDSets(ctx)
			if err != nil {
				_ = grClient.Close()
				return anyOnline, infos, cmpDB, fmt.Errorf("gtid sets %s: %w", pod.Name, err)
			}
			infos = append(infos, groupreplication.GTIDInfo{Pod: pod.Name, Ordinal: podOrdinal, Sets: sets})
			if cmpDB == nil {
				cmpDB, err = sql.Open("mysql", dsn)
				if err != nil {
					_ = grClient.Close()
					return anyOnline, infos, cmpDB, fmt.Errorf("open compare db %s: %w", pod.Name, err)
				}
			}
		}

		_ = grClient.Close()
	}

	return anyOnline, infos, cmpDB, nil
}

// reconcilePods performs bootstrap/join based on current status.
func (r *MySQLReconciler) reconcilePods(
	ctx context.Context,
	pods corev1.PodList,
	mysql *databasev1alpha1.MySQL,
	names resourceNames,
	rootPass, replPass string,
	bootstrapped bool,
	restartCandidate string,
) error {
	statusDirty := false

	for _, pod := range pods.Items {
		if !podReady(&pod) || pod.Status.PodIP == "" {
			continue
		}

		podOrdinal, dsn := buildDSN(pod, names, mysql, rootPass)

		grClient, err := newGRClient(ctx, dsn)
		if err != nil {
			return fmt.Errorf("connect %s: %w", pod.Name, err)
		}

		online, err := grClient.SelfOnline(ctx)
		if err != nil {
			_ = grClient.Close()
			return fmt.Errorf("self online %s: %w", pod.Name, err)
		}
		if online {
			_ = grClient.Close()
			continue
		}

		if err := grClient.EnsureReplicationUser(ctx, replPass); err != nil {
			_ = grClient.Close()
			return fmt.Errorf("ensure replication user %s: %w", pod.Name, err)
		}

		anyOnline, err := grClient.AnyOnlineMember(ctx)
		if err != nil {
			_ = grClient.Close()
			return fmt.Errorf("any online %s: %w", pod.Name, err)
		}

		switch {
		case !bootstrapped && !anyOnline && (mysql.Spec.GroupReplication.ForceBootstrap || podOrdinal == 0):
			if err := grClient.Bootstrap(ctx, replPass); err != nil {
				_ = grClient.Close()
				return fmt.Errorf("bootstrap %s: %w", pod.Name, err)
			}
			bootstrapped = true
			mysql.Status.GroupReplicationBootstrapped = true
			mysql.Status.RestartCandidate = ""
			statusDirty = true
			logf.FromContext(ctx).Info("bootstrapped group replication", "pod:", pod.Name)

		case bootstrapped && restartCandidate != "" && pod.Name == restartCandidate && !anyOnline:
			if err := grClient.Bootstrap(ctx, replPass); err != nil {
				_ = grClient.Close()
				return fmt.Errorf("rebootstrap %s: %w", pod.Name, err)
			}
			mysql.Status.RestartCandidate = ""
			statusDirty = true
			logf.FromContext(ctx).Info("rebootstrapped group replication", "pod:", pod.Name)

		default:
			if err := grClient.Join(ctx, replPass); err != nil {
				_ = grClient.Close()
				return fmt.Errorf("join %s: %w", pod.Name, err)
			}
			logf.FromContext(ctx).Info("joined group replication", "pod:", pod.Name)
		}

		_ = grClient.Close()
	}

	if statusDirty {
		if err := r.Status().Update(ctx, mysql); err != nil {
			logf.FromContext(ctx).Error(err, "status update failed")
		}
	}
	return nil
}

func (r *MySQLReconciler) secretValue(ctx context.Context, ns, name, key string) (string, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: ns,
		Name:      name,
	}, &secret); err != nil {
		return "", err
	}
	value, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("key %s missing in secret %s", key, name)
	}
	return string(value), nil
}

func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func parseOrdinal(podName string) (int, error) {
	parts := strings.Split(podName, "-")
	if len(parts) == 0 {
		return 0, fmt.Errorf("no ordinal")
	}
	return strconv.Atoi(parts[len(parts)-1])
}

// Builds the datasourcename to connect to the DB
//
// https://github.com/go-sql-driver/mysql?tab=readme-ov-file#interpolateparams (Allow usage of ? in CREATE Statements)
// https://github.com/go-sql-driver/mysql?tab=readme-ov-file#timetime-support (time.Time support)
func buildDSN(pod corev1.Pod, names resourceNames, mysql *databasev1alpha1.MySQL, rootPass string) (podOrdinal int, dsn string) {
	podOrdinal, _ = parseOrdinal(pod.Name)
	host := fmt.Sprintf("%s.%s.%s.svc.%s:%d", pod.Name, names.service, mysql.Namespace, clusterDomain, mysql.Spec.Port)
	dsn = fmt.Sprintf("root:%s@tcp(%s)/?parseTime=true&timeout=5s&readTimeout=5s&writeTimeout=5s&interpolateParams=true", rootPass, host)
	return
}
