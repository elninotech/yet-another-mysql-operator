package util

import (
	"testing"

	databasev1alpha1 "github.com/elninotech/yet-another-mysql-operator/api/v1alpha1"
	"github.com/elninotech/yet-another-mysql-operator/internal/controller/util/constants"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestClusterSecretName(t *testing.T) {
	g := gomega.NewWithT(t)

	mysql := &databasev1alpha1.MySQL{
		ObjectMeta: metav1.ObjectMeta{Name: "example"},
		Spec: databasev1alpha1.MySQLSpec{
			RootPasswordSecretName: "custom-secret",
		},
	}

	g.Expect(ClusterSecretName(mysql)).To(gomega.Equal("custom-secret"))

	mysql.Spec.RootPasswordSecretName = ""
	g.Expect(ClusterSecretName(mysql)).To(gomega.Equal("example" + constants.DefaultSecretSuffix))
}

func TestHashConfigMapDataDeterministicAndOrderIndependent(t *testing.T) {
	g := gomega.NewWithT(t)

	data1 := map[string]string{"b": "two", "a": "one"}
	data2 := map[string]string{"a": "one", "b": "two"}

	hash1 := HashConfigMapData(data1)
	hash2 := HashConfigMapData(data2)

	g.Expect(hash1).NotTo(gomega.BeEmpty())
	g.Expect(hash1).To(gomega.Equal(hash2))

	data2["b"] = "different"
	g.Expect(HashConfigMapData(data2)).NotTo(gomega.Equal(hash1))

	data3 := map[string]string{"a": "one", "b": "two", "c": "three"}
	g.Expect(HashConfigMapData(data3)).NotTo(gomega.Equal(hash1))
}
