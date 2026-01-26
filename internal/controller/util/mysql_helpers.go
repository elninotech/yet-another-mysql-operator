package util

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	databasev1alpha1 "github.com/elninotech/mysql-operator/api/v1alpha1"
	"github.com/elninotech/mysql-operator/internal/controller/util/constants"
)

const ConfigHashAnnotation = "mysql-operator/config-hash"

func ClusterSecretName(mysql *databasev1alpha1.MySQL) string {
	if mysql.Spec.RootPasswordSecretName != "" {
		return mysql.Spec.RootPasswordSecretName
	}
	return mysql.Name + constants.DefaultSecretSuffix
}

func HashConfigMapData(data map[string]string) string {
	if len(data) == 0 {
		return ""
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	digest := sha256.New()
	for _, k := range keys {
		digest.Write([]byte(k))
		digest.Write([]byte{0})
		digest.Write([]byte(data[k]))
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}
