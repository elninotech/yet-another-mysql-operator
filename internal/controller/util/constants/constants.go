package constants

const (
	// Root env variable
	RootUserEnv = "MYSQL_ROOT_PASSWORD"

	// Suffix for k8s Secrets
	DefaultSecretSuffix = "-root-credentials"

	ReplicationUserEnv      = "MYSQL_REPLICATION_PASSWORD"
	ReplicationSecretSuffix = "-replication-credentials"

	// Path for default config
	ConfVolumeMountPath = "/etc/mysql"

	// Path to mount DB data volume
	DataVolumeMountPath = "/var/lib/mysql"

	// Default path for extras mysql configs dir
	ConfDPath = "/etc/mysql/conf.d"

	// Identiy.go
	ServerIDOffset           = 1
	IdentityConfFile         = "10-identity.cnf"
	GroupReplicationConfFile = "20-gr.cnf"

	// Images

	// Percona Image
	Image = "percona/percona-server:8.4"

	// Replication RSA Keys
	KeyMountPath = "/etc/mysql/rsa"

	ReplicationRSAPrivateKey = "rsa_private_key.pem"
	ReplicationRSAPublicKey  = "rsa_public_key.pem"
)
