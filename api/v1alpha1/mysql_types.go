package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ConfigMap
type MysqlConf map[string]intstr.IntOrString

// +kubebuilder:validation:Enum=SinglePrimary;MultiPrimary
type GroupReplicationMode string

const (
	GroupReplicationModeSinglePrimary GroupReplicationMode = "SinglePrimary"
	GroupReplicationModeMultiPrimary  GroupReplicationMode = "MultiPrimary"
)

type GroupReplicationSpec struct {
	// +kubebuilder:validation:Pattern=`^[0-9a-fA-F-]{36}$`
	GroupName string `json:"groupName,omitempty"`
	// +kubebuilder:default=SinglePrimary
	Mode                  GroupReplicationMode `json:"mode,omitempty"`
	ReplicationUserSecret string               `json:"replicationUserSecret,omitempty"`
	SeedCount             int32                `json:"seedCount,omitempty"`
	ForceBootstrap        bool                 `json:"forceBootstrap,omitempty"`
	MemberWeight          int32                `json:"memberWeight,omitempty"`
}

// MySQLSpec defines the desired state of MySQL.
type MySQLSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:default="percona/percona-server:8.4"
	Image string `json:"image,omitempty"`
	// +kubebuilder:default="ghcr.io/elninotech/mysql-operator/init:latest"
	InitImage        string `json:"initImage,omitempty"`
	StorageClassName string `json:"storageClassName,omitempty"`
	// +kubebuilder:default="10Gi"
	StorageSize string `json:"storageSize,omitempty"`
	// +kubebuilder:default=3306
	Port                   int32     `json:"port,omitempty"`
	RootPasswordSecretName string    `json:"rootPasswordSecretName,omitempty"`
	MysqlConf              MysqlConf `json:"mysqlConf,omitempty"`
	// +kubebuilder:default=3
	Replicas         int32                `json:"replicas,omitempty"`
	GroupReplication GroupReplicationSpec `json:"groupReplication,omitempty"`
}

// MySQLStatus defines the observed state of MySQL.
type MySQLStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`

	ReadyReplicas                int32  `json:"readyReplicas,omitempty"`
	CurrentReplicas              int32  `json:"currentReplicas,omitempty"`
	GroupReplicationBootstrapped bool   `json:"groupReplicationBootstrapped,omitempty"`
	RestartCandidate             string `json:"restartCandidate,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name=Phase,type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name=Ready,type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name=Current,type=integer,JSONPath=`.status.currentReplicas`
// +kubebuilder:printcolumn:name=GRBootstrapped,type=boolean,JSONPath=`.status.groupReplicationBootstrapped`
// +kubebuilder:printcolumn:name=RestartCandidate,type=string,JSONPath=`.status.restartCandidate`,priority=1
// +kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`

// MySQL is the Schema for the mysqls API.
type MySQL struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MySQLSpec   `json:"spec,omitempty"`
	Status MySQLStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MySQLList contains a list of MySQL.
type MySQLList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MySQL `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MySQL{}, &MySQLList{})
}
