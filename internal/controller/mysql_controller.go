package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	databasev1alpha1 "github.com/elninotech/yet-another-mysql-operator/api/v1alpha1"
	"github.com/elninotech/yet-another-mysql-operator/internal/controller/k8s/configmap"
	"github.com/elninotech/yet-another-mysql-operator/internal/controller/util/constants"

	util "github.com/elninotech/yet-another-mysql-operator/internal/controller/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MySQLReconciler reconciles a MySQL object
type MySQLReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type resourceNames struct {
	cluster string
	secret  string
	replSec string
	config  string
	service string
	sts     string
}

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/status,verbs=get
// +kubebuilder:rbac:groups=database.elnino.tech,resources=mysqls,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=database.elnino.tech,resources=mysqls/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=database.elnino.tech,resources=mysqls/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets;configmaps;persistentvolumeclaims;services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets/status;configmaps/status;persistentvolumeclaims/status;services/status,verbs=get
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MySQLReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	var mysql databasev1alpha1.MySQL

	if err := r.Get(ctx, req.NamespacedName, &mysql); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	names := buildResourceNames(&mysql)

	if err := r.reconcileRootSecret(ctx, &mysql, names.secret); err != nil {
		return r.handleReconcileError(ctx, &mysql, "Secret", names.secret, err)
	}

	if err := r.reconcileReplicationSecret(ctx, &mysql, names.replSec); err != nil {
		return r.handleReconcileError(ctx, &mysql, "Replication Secret", names.replSec, err)
	}

	if err := r.reconcileConfigMap(ctx, &mysql, names.config); err != nil {
		return r.handleReconcileError(ctx, &mysql, "ConfigMap", names.config, err)
	}

	var cfg corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Name: names.config, Namespace: mysql.Namespace}, &cfg); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ConfigMap not yet observable; requeueing", "name", names.config)
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return r.failStatus(ctx, &mysql, err, "Loading ConfigMap")
	}

	cfgHash := util.HashConfigMapData(cfg.Data)

	if err := r.reconcileService(ctx, &mysql, names.service); err != nil {
		return r.handleReconcileError(ctx, &mysql, "Service", names.service, err)
	}

	if err := r.reconcileStatefulSet(ctx, &mysql, names.sts, names.secret, names.replSec, names.config, cfgHash); err != nil {
		if apierrors.IsConflict(err) {
			logger.Info("StatefulSet changed during reconcile; will retry", "name", names.sts)
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		return r.failStatus(ctx, &mysql, err, "Reconciling StatefulSet")
	}

	var sts appsv1.StatefulSet
	if err := r.Get(ctx, types.NamespacedName{Name: names.sts, Namespace: mysql.Namespace}, &sts); err == nil {
		desired := mysql.Spec.Replicas
		if sts.Spec.Replicas != nil {
			desired = *sts.Spec.Replicas
		}
		mysql.Status.CurrentReplicas = desired
		mysql.Status.ReadyReplicas = sts.Status.ReadyReplicas
		if sts.Status.ReadyReplicas == desired {
			mysql.Status.Phase = "Ready"
			mysql.Status.Message = "MySQL pod is ready"
		} else {
			mysql.Status.Phase = "Pending"
			mysql.Status.Message = "Waiting for pod readiness"
		}
		desiredStatus := mysql.Status
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest databasev1alpha1.MySQL
			if err := r.Get(ctx, req.NamespacedName, &latest); err != nil {
				return err
			}
			latest.Status = desiredStatus
			return r.Status().Update(ctx, &latest)
		}); err != nil {
			logger.Error(err, "status update failed after retry")
		}

		// Groupreplication Startup
		if sts.Status.ReadyReplicas == desired {
			if err := r.reconcileGroupReplication(ctx, &mysql, names); err != nil {
				return r.failStatus(ctx, &mysql, err, "Reconciling Group Replication")
			}
		}
	}

	return ctrl.Result{}, nil
}

// reconcileConfigMap ensures that the configmap set under configmap/config_map.go
// is created. This will generate the my.cnf file.
func (r *MySQLReconciler) reconcileConfigMap(ctx context.Context, mysql *databasev1alpha1.MySQL, name string) error {
	cfg := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: mysql.Namespace,
	}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cfg, func() error {
		if err := controllerutil.SetControllerReference(mysql, cfg, r.Scheme); err != nil {
			return err
		}
		if cfg.Data == nil {
			cfg.Data = map[string]string{}
		}
		cfg.Data["my.cnf"] = configmap.RenderMySQLConfig(nil, mysql.Spec.MysqlConf)

		return nil
	})
	return err
}

// reconcileService ensures a headless Service,
// sets the owner refs to the controller, selector, MySQL port, and enabling publish-not-ready
// so that the GCS for group replication won't report communication errors due to the DNS not being able to be resolved.
func (r *MySQLReconciler) reconcileService(ctx context.Context, mysql *databasev1alpha1.MySQL, name string) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: mysql.Namespace,
		Labels:    map[string]string{"app": mysql.Name},
	}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(mysql, svc, r.Scheme); err != nil {
			return err
		}
		svc.Spec.Selector = map[string]string{"app": mysql.Name}
		svc.Spec.Ports = []corev1.ServicePort{{
			Name: "mysql", Port: mysql.Spec.Port, TargetPort: intstr.FromInt32(mysql.Spec.Port),
		}}
		svc.Spec.ClusterIP = corev1.ClusterIPNone // https://kubernetes.io/docs/concepts/services-networking/service/#headless-services
		return nil
	})
	return err
}

// reconcileStatefulSet is the template what a MySQL pod should look like. This will be reconciled to the defined state in the CRD.
func (r *MySQLReconciler) reconcileStatefulSet(ctx context.Context, mysql *databasev1alpha1.MySQL, name, secretName, replSecretName, cfgName, configHash string) error {
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mysql.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sts, func() error {
		if err := controllerutil.SetControllerReference(mysql, sts, r.Scheme); err != nil {
			return err
		}
		replicas := mysql.Spec.Replicas
		sts.Spec.Replicas = &replicas
		sts.Spec.ServiceName = mysql.Name
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": mysql.Name}}
		if sts.Spec.Template.Annotations == nil {
			sts.Spec.Template.Annotations = map[string]string{}
		}
		sts.Spec.Template.Labels = map[string]string{"app": mysql.Name}
		selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": mysql.Name}}
		sts.Spec.Template.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{
			{
				MaxSkew:           1,
				TopologyKey:       "kubernetes.io/hostname",
				WhenUnsatisfiable: corev1.DoNotSchedule,
				LabelSelector:     selector,
			},
		}
		sts.Spec.Template.Spec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						TopologyKey:   "kubernetes.io/hostname",
						LabelSelector: selector,
					},
				}},
			},
		}
		runAs := int64(999)
		fsGrp := int64(999)
		policy := corev1.FSGroupChangeOnRootMismatch
		sts.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsUser:           &runAs,
			RunAsGroup:          &runAs,
			FSGroup:             &fsGrp,
			FSGroupChangePolicy: &policy,
		}
		sts.Spec.Template.Annotations[util.ConfigHashAnnotation] = configHash
		sts.Spec.Template.Spec.InitContainers = []corev1.Container{{
			Name:            "bootstrap-conf-d",
			Image:           mysql.Spec.InitImage,
			ImagePullPolicy: corev1.PullAlways,
			Env: []corev1.EnvVar{
				{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
				{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
				{Name: "POD_IP", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"}}},
				{Name: "GR_REPLICAS", Value: fmt.Sprintf("%d", mysql.Spec.Replicas)},
				{Name: "MYSQL_PORT", Value: fmt.Sprintf("%d", mysql.Spec.Port)},
				{Name: "GR_GROUP_NAME", Value: mysql.Spec.GroupReplication.GroupName},
			},
			VolumeMounts: []corev1.VolumeMount{{Name: "conf-d", MountPath: "/etc/mysql/conf.d"}},
		},
		}
		sts.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name:  "mysql",
				Image: mysql.Spec.Image,
				Ports: []corev1.ContainerPort{{Name: "mysql", ContainerPort: mysql.Spec.Port}},
				Env: []corev1.EnvVar{
					{
						Name: constants.RootUserEnv,
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
								Key:                  constants.RootUserEnv,
							},
						},
					},
					{
						Name: constants.ReplicationUserEnv,
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: replSecretName},
								Key:                  constants.ReplicationUserEnv,
							},
						},
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "run", MountPath: "/var/run/mysqld"},
					{Name: "data", MountPath: constants.DataVolumeMountPath},
					{Name: "conf", MountPath: constants.ConfVolumeMountPath},
					{Name: "conf-d", MountPath: constants.ConfDPath},
					{Name: "replication-rsa", MountPath: constants.KeyMountPath, ReadOnly: true},
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						Exec: &corev1.ExecAction{
							Command: []string{
								"sh",
								"-c",
								`MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --protocol=socket --connect-timeout=3 -uroot -Nse "SELECT 1"`,
							},
						},
					},
					InitialDelaySeconds: 30,
					PeriodSeconds:       10,
					TimeoutSeconds:      5,
					FailureThreshold:    6,
				},
				LivenessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						Exec: &corev1.ExecAction{
							Command: []string{
								"sh",
								"-c",
								`MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqladmin --protocol=socket --connect-timeout=3 --silent -uroot ping`,
							},
						},
					},
					InitialDelaySeconds: 60,
					PeriodSeconds:       10,
					TimeoutSeconds:      5,
					FailureThreshold:    6,
				},
			},
		}
		sts.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "conf",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: cfgName},
						Items:                []corev1.KeyToPath{{Key: "my.cnf", Path: "my.cnf"}},
					},
				},
			},
			{
				Name: "conf-d",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
			{
				Name: "replication-rsa",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: replSecretName,
						Items: []corev1.KeyToPath{
							{Key: constants.ReplicationRSAPrivateKey, Path: constants.ReplicationRSAPrivateKey},
							{Key: constants.ReplicationRSAPublicKey, Path: constants.ReplicationRSAPublicKey},
						},
					},
				},
			},
			{
				Name: "run",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		}
		pvc := corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "data"},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(mysql.Spec.StorageSize),
					},
				},
			},
		}
		if mysql.Spec.StorageClassName != "" {
			pvc.Spec.StorageClassName = &mysql.Spec.StorageClassName
		}
		sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{pvc}
		return nil
	})
	return err
}

// buildResourceNames returnes the names for MySQL owned resources
func buildResourceNames(mysql *databasev1alpha1.MySQL) resourceNames {
	base := mysql.Name
	return resourceNames{
		cluster: base,
		secret:  util.ClusterSecretName(mysql),
		replSec: replicationSecretName(mysql),
		config:  fmt.Sprintf("%s-mycnf", base),
		service: base,
		sts:     base,
	}
}

// Returns failstatus
func (r *MySQLReconciler) failStatus(ctx context.Context, mysql *databasev1alpha1.MySQL, err error, where string) (ctrl.Result, error) {
	mysql.Status.Phase = "Degraded"
	mysql.Status.Message = fmt.Sprintf("%s: %v", where, err)
	_ = r.Status().Update(ctx, mysql)
	return ctrl.Result{}, err
}

// handleReconcileError standardizes reconcile errors, requeueing on conflicts and otherwise setting a failed status.
func (r *MySQLReconciler) handleReconcileError(
	ctx context.Context,
	mysql *databasev1alpha1.MySQL,
	k8sresource string,
	objName string,
	err error,
) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	if apierrors.IsConflict(err) {
		logger.Info(fmt.Sprintf("%s changed during reconcile; will retry", k8sresource), "name", objName)
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return r.failStatus(ctx, mysql, err, fmt.Sprintf("Reconciling %s", k8sresource))
}

// SetupWithManager sets up the controller with the Manager.
func (r *MySQLReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasev1alpha1.MySQL{}).
		Named("mysql").
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.StatefulSet{}).
		Complete(r)
}
