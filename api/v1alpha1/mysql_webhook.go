package v1alpha1

import (
	"context"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

func (r *MySQL) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&MySQL{}).
		WithDefaulter(&MySQL{}). // ensure the mutating webhook is registered
		Complete()
}

var _ webhook.CustomDefaulter = &MySQL{}

// +kubebuilder:webhook:path=/mutate-database-elnino-tech-v1alpha1-mysql,mutating=true,failurePolicy=Fail,sideEffects=None,groups=database.elnino.tech,resources=mysqls,verbs=create;update,versions=v1alpha1,name=mmysql.database.elnino.tech,admissionReviewVersions=v1
// Default sets runtime defaults on the incoming object.
func (r *MySQL) Default(ctx context.Context, obj runtime.Object) error {
	mysql, ok := obj.(*MySQL)
	if !ok {
		return nil
	}

	if mysql.Spec.Image == "" {
		mysql.Spec.Image = "percona/percona-server:8.4"
	}
	if mysql.Spec.StorageSize == "" {
		mysql.Spec.StorageSize = "10Gi"
	}
	if mysql.Spec.Port == 0 {
		mysql.Spec.Port = 3306
	}
	if mysql.Spec.Replicas == 0 {
		mysql.Spec.Replicas = 3
	}
	if mysql.Spec.GroupReplication.Mode == "" {
		mysql.Spec.GroupReplication.Mode = GroupReplicationModeSinglePrimary
	}
	if mysql.Spec.GroupReplication.GroupName == "" {
		mysql.Spec.GroupReplication.GroupName = uuid.NewString()
	}
	return nil
}
