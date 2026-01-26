package controller

import (
	"context"
	"strings"
	"sync"

	databasev1alpha1 "github.com/elninotech/mysql-operator/api/v1alpha1"
	groupreplication "github.com/elninotech/mysql-operator/internal/controller/mysql/groupreplication"
	"github.com/elninotech/mysql-operator/internal/controller/util/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("GroupReplication reconciliation", func() {
	var (
		origFactory func(context.Context, string) (grClient, error)
		factory     *recordingGRFactory
		mysql       *databasev1alpha1.MySQL
		names       resourceNames
	)

	BeforeEach(func() {
		origFactory = newGRClient
		factory = newRecordingGRFactory()
		newGRClient = factory.New

		mysql = &databasev1alpha1.MySQL{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "demo",
				Namespace: "default",
			},
			Spec: databasev1alpha1.MySQLSpec{
				Port:     3306,
				Replicas: 2,
				GroupReplication: databasev1alpha1.GroupReplicationSpec{
					ForceBootstrap: true,
				},
			},
		}
		Expect(k8sClient.Create(ctx, mysql)).To(Succeed())
		names = buildResourceNames(mysql)

		// prerequisites for loadSecretsAndPods
		createSecretWithData(names.secret, constants.RootUserEnv, []byte("rootpw"))
		createSecretWithData(names.replSec, constants.ReplicationUserEnv, []byte("replpw"))

		createReadyPod(names.cluster+"-0", mysql.Namespace, mysql.Name, "10.0.0.1")
		createReadyPod(names.cluster+"-1", mysql.Namespace, mysql.Name, "10.0.0.2")
	})

	AfterEach(func() {
		newGRClient = origFactory
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, mysql))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: names.secret, Namespace: mysql.Namespace}}))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: names.replSec, Namespace: mysql.Namespace}}))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: names.cluster + "-0", Namespace: mysql.Namespace}}))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: names.cluster + "-1", Namespace: mysql.Namespace}}))).To(Succeed())
	})

	It("bootstraps the first ready pod and joins the rest", func() {
		r := &MySQLReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}

		Expect(r.reconcileGroupReplication(ctx, mysql, names)).To(Succeed())

		var updated databasev1alpha1.MySQL
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mysql.Name, Namespace: mysql.Namespace}, &updated)).To(Succeed())
		Expect(updated.Status.GroupReplicationBootstrapped).To(BeTrue())
		Expect(factory.calls["demo-0"].bootstrapCalled).To(BeTrue())
		Expect(factory.calls["demo-1"].joinCalled).To(BeTrue())
		Expect(factory.calls["demo-0"].ensureUserCalled).To(BeTrue())
		Expect(factory.calls["demo-1"].ensureUserCalled).To(BeTrue())
	})

	It("rebootstraps the chosen restart candidate when none are online", func() {
		mysql.Status.GroupReplicationBootstrapped = true
		mysql.Status.RestartCandidate = names.cluster + "-1"
		Expect(k8sClient.Status().Update(ctx, mysql)).To(Succeed())

		var refreshed databasev1alpha1.MySQL
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mysql.Name, Namespace: mysql.Namespace}, &refreshed)).To(Succeed())

		r := &MySQLReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		Expect(r.reconcileGroupReplication(ctx, &refreshed, names)).To(Succeed())

		var updated databasev1alpha1.MySQL
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mysql.Name, Namespace: mysql.Namespace}, &updated)).To(Succeed())
		Expect(updated.Status.RestartCandidate).To(BeEmpty())
		Expect(factory.calls[names.cluster+"-1"].bootstrapCalled).To(BeTrue())
	})
})

// ---- fakes & helpers ----

type recordingGRFactory struct {
	mu        sync.Mutex
	anyOnline bool
	calls     map[string]*recordingGRClient
}

func newRecordingGRFactory() *recordingGRFactory {
	return &recordingGRFactory{calls: map[string]*recordingGRClient{}}
}

func (f *recordingGRFactory) New(_ context.Context, dsn string) (grClient, error) {
	pod := podNameFromDSN(dsn)
	f.mu.Lock()
	defer f.mu.Unlock()
	client := &recordingGRClient{pod: pod, factory: f}
	f.calls[pod] = client
	return client, nil
}

type recordingGRClient struct {
	pod              string
	factory          *recordingGRFactory
	bootstrapCalled  bool
	joinCalled       bool
	ensureUserCalled bool
}

func (c *recordingGRClient) Close() error { return nil }

func (c *recordingGRClient) SelfOnline(context.Context) (bool, error) { return false, nil }

func (c *recordingGRClient) GTIDSets(context.Context) (groupreplication.GTIDSets, error) {
	return groupreplication.GTIDSets{}, nil
}

func (c *recordingGRClient) EnsureReplicationUser(context.Context, string) error {
	c.ensureUserCalled = true
	return nil
}

func (c *recordingGRClient) AnyOnlineMember(context.Context) (bool, error) {
	return c.factory.anyOnline, nil
}

func (c *recordingGRClient) Bootstrap(context.Context, string) error {
	c.bootstrapCalled = true
	c.factory.anyOnline = true
	return nil
}

func (c *recordingGRClient) Join(context.Context, string) error {
	c.joinCalled = true
	return nil
}

func createSecretWithData(name, key string, value []byte) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data:       map[string][]byte{key: value},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
}

func createReadyPod(name, namespace, appLabel, ip string) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": appLabel},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "mysql",
				Image: "mysql:8.0",
			}},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())

	pod.Status.PodIP = ip
	pod.Status.Conditions = []corev1.PodCondition{{
		Type:   corev1.PodReady,
		Status: corev1.ConditionTrue,
	}}
	Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
}

func podNameFromDSN(dsn string) string {
	start := strings.Index(dsn, "tcp(")
	if start == -1 {
		return ""
	}
	start += len("tcp(")
	rest := dsn[start:]
	end := strings.Index(rest, ")")
	if end == -1 {
		return ""
	}
	hostPort := rest[:end]
	return strings.Split(hostPort, ".")[0]
}
