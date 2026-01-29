package controller

import (
	"context"

	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"

	"encoding/base64"
	"encoding/pem"

	databasev1alpha1 "github.com/elninotech/yet-another-mysql-operator/api/v1alpha1"
	"github.com/elninotech/yet-another-mysql-operator/internal/controller/util/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *MySQLReconciler) reconcileRootSecret(ctx context.Context, mysql *databasev1alpha1.MySQL, name string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mysql.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(mysql, secret, r.Scheme); err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		if _, ok := secret.Data[constants.RootUserEnv]; !ok {
			pw := make([]byte, 20)
			_, _ = rand.Read(pw)
			secret.Data[constants.RootUserEnv] = []byte(base64.StdEncoding.EncodeToString(pw))
		}
		return nil
	})
	return err
}

func (r *MySQLReconciler) reconcileReplicationSecret(ctx context.Context, mysql *databasev1alpha1.MySQL, name string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: mysql.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(mysql, secret, r.Scheme); err != nil {
			return err
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		if _, ok := secret.Data[constants.ReplicationUserEnv]; !ok {
			pw := make([]byte, 20)
			_, _ = rand.Read(pw)
			secret.Data[constants.ReplicationUserEnv] = []byte(base64.StdEncoding.EncodeToString(pw))
		}
		if secret.Data[constants.ReplicationRSAPrivateKey] == nil || secret.Data[constants.ReplicationRSAPublicKey] == nil {
			privPEM, pubPEM, genErr := generateReplicationRSAKeyPair()
			if genErr != nil {
				return genErr
			}
			secret.Data[constants.ReplicationRSAPrivateKey] = privPEM
			secret.Data[constants.ReplicationRSAPublicKey] = pubPEM
		}
		return nil
	})
	return err
}

func replicationSecretName(mysql *databasev1alpha1.MySQL) string {
	if mysql.Spec.GroupReplication.ReplicationUserSecret != "" {
		return mysql.Spec.GroupReplication.ReplicationUserSecret
	}
	return mysql.Name + constants.ReplicationSecretSuffix
}

func generateReplicationRSAKeyPair() ([]byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	})
	return privPEM, pubPEM, nil
}
