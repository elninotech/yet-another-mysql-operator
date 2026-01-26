package configmap

import (
	"fmt"
	"sort"
	"strings"

	databasev1alpha1 "github.com/elninotech/mysql-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type MysqlConf map[string]intstr.IntOrString

func RenderMySQLConfig(base map[string]string, overrides databasev1alpha1.MysqlConf) string {
	effective := make(map[string]string, len(defaultMyCnf)+len(base)+len(overrides))

	source := base
	if len(source) == 0 {
		source = defaultMyCnf
	}

	for k, v := range source {
		effective[k] = v
	}

	for _, flag := range defaultMyCnfBooleanFlags {
		if _, exists := effective[flag]; !exists {
			effective[flag] = ""
		}
	}

	for k, v := range overrides {
		effective[k] = v.String()
	}

	keys := make([]string, 0, len(effective))
	for k := range effective {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("[mysqld]\n")
	for _, k := range keys {
		if effective[k] == "" {
			fmt.Fprintf(&b, "%s\n", k)
			continue
		}
		fmt.Fprintf(&b, "%s = %s\n", k, effective[k])
	}
	b.WriteString("!includedir /etc/mysql/conf.d\n")

	return b.String()
}

var (
	defaultMyCnf = map[string]string{
		"binlog_expire_logs_seconds":                       "1209600",
		"sql-mode":                                         "STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_VALUE_ON_ZERO,NO_ENGINE_SUBSTITUTION,NO_ZERO_DATE,NO_ZERO_IN_DATE,ONLY_FULL_GROUP_BY",
		"character-set-server":                             "utf8mb4",
		"collation-server":                                 "utf8mb4_unicode_ci",
		"default-storage-engine":                           "InnoDB",
		"enforce-gtid-consistency":                         "on",
		"gtid-mode":                                        "on",
		"innodb-file-per-table":                            "1",
		"innodb-flush-log-at-trx-commit":                   "2",
		"innodb-flush-method":                              "O_DIRECT",
		"innodb-redo_log_capacity":                         "2",
		"key-buffer-size":                                  "32M",
		"log-bin":                                          "/var/lib/mysql/mysql-bin",
		"log-replica-updates":                              "on",
		"max-allowed-packet":                               "16M",
		"max-connect-errors":                               "1000000",
		"max-connections":                                  "500",
		"max-heap-table-size":                              "32M",
		"myisam-recover-options":                           "FORCE,BACKUP",
		"open-files-limit":                                 "65535",
		"relay-log-recovery":                               "on",
		"skip-replica-start":                               "off",
		"sync-binlog":                                      "1",
		"sysdate-is-now":                                   "1",
		"table-definition-cache":                           "4096",
		"table-open-cache":                                 "4096",
		"thread-cache-size":                                "50",
		"tmp-table-size":                                   "32M",
		"binlog-space-limit":                               "16G",
		"disabled-storage-engines":                         "MyISAM",
		"innodb-buffer-pool-instances":                     "4",
		"innodb-buffer-pool-size":                          "512M",
		"innodb-log-buffer-size":                           "64M",
		"innodb-redo-log-capacity":                         "1G",
		"join-buffer-size":                                 "4M",
		"max-binlog-size":                                  "1G",
		"sort-buffer-size":                                 "4M",
		"caching_sha2_password_private_key_path":           "/etc/mysql/rsa/rsa_private_key.pem",
		"caching_sha2_password_public_key_path":            "/etc/mysql/rsa/rsa_public_key.pem",
		"caching_sha2_password_auto_generate_rsa_keys":     "OFF",
		"loose-group_replication_recovery_public_key_path": "/etc/mysql/rsa/rsa_public_key.pem",
		"loose-group_replication_recovery_use_ssl":         "OFF",
	}
	defaultMyCnfBooleanFlags = []string{"skip-name-resolve"}
)
