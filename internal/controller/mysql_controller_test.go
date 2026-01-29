package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	databasev1alpha1 "github.com/elninotech/yet-another-mysql-operator/api/v1alpha1"
	"github.com/elninotech/yet-another-mysql-operator/internal/controller/util"
	"github.com/elninotech/yet-another-mysql-operator/internal/controller/util/constants"
)

var _ = Describe("MySQL Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		mysql := &databasev1alpha1.MySQL{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind MySQL")
			err := k8sClient.Get(ctx, typeNamespacedName, mysql)
			if err != nil && errors.IsNotFound(err) {
				resource := &databasev1alpha1.MySQL{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: databasev1alpha1.MySQLSpec{
						StorageClassName:       "standard",
						RootPasswordSecretName: resourceName + "-root-credentials",
						MysqlConf: databasev1alpha1.MysqlConf{
							"log-bin":   intstr.FromString("/var/lib/mysql/binlog"),
							"log-error": intstr.FromString("/var/log/mysql/error.log"),
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &databasev1alpha1.MySQL{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			if errors.IsNotFound(err) {
				resource = nil
			} else {
				Expect(err).NotTo(HaveOccurred())
			}

			if resource != nil {
				By("Cleanup the specific resource instance MySQL")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			for _, secretName := range []string{
				fmt.Sprintf("%s%s", resourceName, constants.DefaultSecretSuffix),
				resourceName + "-root-credentials",
			} {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: "default"},
				}))).To(Succeed())
			}

			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-mycnf", resourceName), Namespace: "default"},
			}))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
			}))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"},
			}))).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &MySQLReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			fetched := &databasev1alpha1.MySQL{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, fetched)).To(Succeed())
			Expect(fetched.Status.Phase).To(Equal("Pending"))
			Expect(fetched.Status.Message).To(ContainSubstring("Waiting for pod readiness"))

			secretName := util.ClusterSecretName(fetched)
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: fetched.Namespace}, secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey(constants.RootUserEnv))
			Expect(secret.Data[constants.RootUserEnv]).NotTo(BeEmpty())

			cfgName := fmt.Sprintf("%s-mycnf", fetched.Name)
			cfg := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cfgName, Namespace: fetched.Namespace}, cfg)).To(Succeed())
			Expect(cfg.Data).To(HaveKey("my.cnf"))
			Expect(cfg.Data["my.cnf"]).To(ContainSubstring("log-error = /var/log/mysql/error.log"))
			Expect(cfg.Data["my.cnf"]).To(ContainSubstring("skip-name-resolve\n"))

			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fetched.Name, Namespace: fetched.Namespace}, svc)).To(Succeed())
			Expect(svc.Spec.Selector).To(HaveKeyWithValue("app", fetched.Name))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(3306)))
			Expect(svc.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(3306)))

			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fetched.Name, Namespace: fetched.Namespace}, sts)).To(Succeed())
			Expect(sts.Spec.Template.Annotations["mysql-operator/config-hash"]).To(Equal(util.HashConfigMapData(cfg.Data)))
			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := sts.Spec.Template.Spec.Containers[0]
			Expect(container.Image).To(Equal(fetched.Spec.Image))
			Expect(container.VolumeMounts).To(ContainElement(corev1.VolumeMount{Name: "data", MountPath: constants.DataVolumeMountPath}))
			Expect(container.VolumeMounts).To(ContainElement(corev1.VolumeMount{Name: "conf", MountPath: constants.ConfVolumeMountPath}))
			Expect(container.VolumeMounts).To(ContainElement(corev1.VolumeMount{Name: "conf-d", MountPath: constants.ConfDPath}))

			Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))
			template := sts.Spec.VolumeClaimTemplates[0]
			Expect(template.Name).To(Equal("data"))
			storageQty, ok := template.Spec.Resources.Requests[corev1.ResourceStorage]
			Expect(ok).To(BeTrue())
			Expect(storageQty.String()).To(Equal("10Gi"))

			var rootEnv corev1.EnvVar
			for _, env := range container.Env {
				if env.Name == constants.RootUserEnv {
					rootEnv = env
					break
				}
			}
			Expect(rootEnv.Name).To(Equal(constants.RootUserEnv))
			Expect(rootEnv.ValueFrom).NotTo(BeNil())
			Expect(rootEnv.ValueFrom.SecretKeyRef).NotTo(BeNil())
			Expect(rootEnv.ValueFrom.SecretKeyRef.Name).To(Equal(secretName))
			Expect(rootEnv.ValueFrom.SecretKeyRef.Key).To(Equal(constants.RootUserEnv))

			var confVolume *corev1.Volume
			for i := range sts.Spec.Template.Spec.Volumes {
				if sts.Spec.Template.Spec.Volumes[i].Name == "conf" {
					confVolume = &sts.Spec.Template.Spec.Volumes[i]
					break
				}
			}
			Expect(confVolume).NotTo(BeNil())
			Expect(confVolume.VolumeSource.ConfigMap).NotTo(BeNil())
			Expect(confVolume.VolumeSource.ConfigMap.LocalObjectReference.Name).To(Equal(cfg.Name))
			Expect(confVolume.VolumeSource.ConfigMap.Items).To(
				ContainElement(corev1.KeyToPath{Key: "my.cnf", Path: "my.cnf"}),
			)

			var confDVolume *corev1.Volume
			for i := range sts.Spec.Template.Spec.Volumes {
				if sts.Spec.Template.Spec.Volumes[i].Name == "conf-d" {
					confDVolume = &sts.Spec.Template.Spec.Volumes[i]
					break
				}
			}
			Expect(confDVolume).NotTo(BeNil())
			Expect(confDVolume.VolumeSource.EmptyDir).NotTo(BeNil())
		})

		It("should apply defaults when optional spec fields are empty", func() {
			controllerReconciler := &MySQLReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			Eventually(func() error {
				return k8sClient.Get(ctx, typeNamespacedName, mysql)
			}).Should(Succeed())

			mysql.Spec.Image = ""
			mysql.Spec.StorageSize = ""
			mysql.Spec.StorageClassName = ""
			mysql.Spec.Port = 0
			mysql.Spec.RootPasswordSecretName = ""
			mysql.Spec.MysqlConf = nil
			Expect(k8sClient.Update(ctx, mysql)).To(Succeed())

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mysql.Name, Namespace: mysql.Namespace}, svc)).To(Succeed())
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(mysql.Spec.Port))
			Expect(svc.Spec.Ports[0].TargetPort.IntVal).To(Equal(mysql.Spec.Port))

			cfg := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s-mycnf", mysql.Name), Namespace: mysql.Namespace}, cfg)).To(Succeed())
			Expect(cfg.Data).To(HaveKey("my.cnf"))
			Expect(cfg.Data["my.cnf"]).To(ContainSubstring("innodb-flush-log-at-trx-commit = 2"))

			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mysql.Name, Namespace: mysql.Namespace}, sts)).To(Succeed())
			Expect(sts.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := sts.Spec.Template.Spec.Containers[0]
			Expect(container.Image).To(Equal(constants.Image))
			Expect(container.Ports).To(HaveLen(1))
			Expect(container.Ports[0].ContainerPort).To(Equal(mysql.Spec.Port))

			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("%s%s", mysql.Name, constants.DefaultSecretSuffix), Namespace: mysql.Namespace}, secret)).To(Succeed())
			Expect(secret.Data).To(HaveKey(constants.RootUserEnv))
		})

		It("should mark the instance ready when the statefulset has ready replicas", func() {
			controllerReconciler := &MySQLReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: "default"}, sts)).To(Succeed())
			sts.Status.Replicas = *sts.Spec.Replicas
			sts.Status.ReadyReplicas = *sts.Spec.Replicas
			sts.Status.CurrentReplicas = *sts.Spec.Replicas
			sts.Status.UpdatedReplicas = *sts.Spec.Replicas
			sts.Status.ObservedGeneration = sts.Generation
			Expect(k8sClient.Status().Update(ctx, sts)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() (string, error) {
				fetched := &databasev1alpha1.MySQL{}
				if err := k8sClient.Get(ctx, typeNamespacedName, fetched); err != nil {
					return "", err
				}
				return fetched.Status.Phase, nil
			}).Should(Equal("Ready"))

			fetched := &databasev1alpha1.MySQL{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, fetched)).To(Succeed())
			Expect(fetched.Status.Message).To(Equal("MySQL pod is ready"))
		})

		It("should ignore reconciliation when the MySQL resource is missing", func() {
			controllerReconciler := &MySQLReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "does-not-exist",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("should mark the instance degraded when secret reconciliation fails", func() {
			controllerReconciler := &MySQLReconciler{
				Client: &hookClient{
					Client: k8sClient,
					createHook: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) (bool, error) {
						if _, ok := obj.(*corev1.Secret); ok {
							return true, fmt.Errorf("secret create failure")
						}
						return false, nil
					},
				},
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())

			Eventually(func(g Gomega) {
				current := &databasev1alpha1.MySQL{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, current)).To(Succeed())
				g.Expect(current.Status.Phase).To(Equal("Degraded"))
				g.Expect(current.Status.Message).To(ContainSubstring("Reconciling Secret"))
			}).Should(Succeed())
		})

		It("should mark the instance degraded when configmap reconciliation fails", func() {
			controllerReconciler := &MySQLReconciler{
				Client: &hookClient{
					Client: k8sClient,
					createHook: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) (bool, error) {
						if _, ok := obj.(*corev1.ConfigMap); ok {
							return true, fmt.Errorf("configmap create failure")
						}
						return false, nil
					},
				},
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())

			Eventually(func(g Gomega) {
				current := &databasev1alpha1.MySQL{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, current)).To(Succeed())
				g.Expect(current.Status.Phase).To(Equal("Degraded"))
				g.Expect(current.Status.Message).To(ContainSubstring("Reconciling ConfigMap"))
			}).Should(Succeed())
		})

		It("should requeue when the configmap is not yet observable", func() {
			cfgName := fmt.Sprintf("%s-mycnf", resourceName)
			hook := &configMapGetHook{
				target: types.NamespacedName{Name: cfgName, Namespace: "default"},
			}
			controllerReconciler := &MySQLReconciler{
				Client: &hookClient{
					Client:  k8sClient,
					getHook: hook.Handle,
					createHook: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) (bool, error) {
						return false, nil
					},
				},
				Scheme: k8sClient.Scheme(),
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		})

		It("should mark the instance degraded when service reconciliation fails", func() {
			controllerReconciler := &MySQLReconciler{
				Client: &hookClient{
					Client: k8sClient,
					createHook: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) (bool, error) {
						if _, ok := obj.(*corev1.Service); ok {
							return true, fmt.Errorf("service create failure")
						}
						return false, nil
					},
				},
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())

			Eventually(func(g Gomega) {
				current := &databasev1alpha1.MySQL{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, current)).To(Succeed())
				g.Expect(current.Status.Phase).To(Equal("Degraded"))
				g.Expect(current.Status.Message).To(ContainSubstring("Reconciling Service"))
			}).Should(Succeed())
		})

		It("should requeue when statefulset reconciliation hits a conflict", func() {
			baseReconciler := &MySQLReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := baseReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			controllerReconciler := &MySQLReconciler{
				Client: &hookClient{
					Client: k8sClient,
					updateHook: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) (bool, error) {
						if _, ok := obj.(*appsv1.StatefulSet); ok {
							return true, errors.NewConflict(
								schema.GroupResource{Group: "apps", Resource: "statefulsets"},
								obj.GetName(),
								fmt.Errorf("statefulset update conflict"),
							)
						}
						return false, nil
					},
				},
				Scheme: k8sClient.Scheme(),
			}

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		})

		It("should mark the instance degraded when statefulset reconciliation fails", func() {
			controllerReconciler := &MySQLReconciler{
				Client: &hookClient{
					Client: k8sClient,
					createHook: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) (bool, error) {
						if _, ok := obj.(*appsv1.StatefulSet); ok {
							return true, fmt.Errorf("statefulset create failure")
						}
						return false, nil
					},
				},
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).To(HaveOccurred())

			Eventually(func(g Gomega) {
				current := &databasev1alpha1.MySQL{}
				Expect(k8sClient.Get(ctx, typeNamespacedName, current)).To(Succeed())
				g.Expect(current.Status.Phase).To(Equal("Degraded"))
				g.Expect(current.Status.Message).To(ContainSubstring("Reconciling StatefulSet"))
			}).Should(Succeed())
		})
	})
})

var _ = Describe("hashConfigMapData", func() {
	It("returns an empty string when the data map is empty", func() {
		Expect(util.HashConfigMapData(map[string]string{})).To(BeEmpty())
	})
})

type hookClient struct {
	client.Client
	getHook    func(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) (bool, error)
	createHook func(ctx context.Context, obj client.Object, opts ...client.CreateOption) (bool, error)
	updateHook func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) (bool, error)
}

func (c *hookClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.getHook != nil {
		if handled, err := c.getHook(ctx, key, obj, opts...); handled {
			return err
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *hookClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if c.createHook != nil {
		if handled, err := c.createHook(ctx, obj, opts...); handled {
			return err
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *hookClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if c.updateHook != nil {
		if handled, err := c.updateHook(ctx, obj, opts...); handled {
			return err
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}

type configMapGetHook struct {
	target types.NamespacedName
	count  int
}

func (h *configMapGetHook) Handle(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) (bool, error) {
	if key.Name == h.target.Name && key.Namespace == h.target.Namespace {
		h.count++
		if h.count == 2 {
			return true, errors.NewNotFound(
				schema.GroupResource{Group: "", Resource: "configmaps"},
				key.Name,
			)
		}
	}
	return false, nil
}
