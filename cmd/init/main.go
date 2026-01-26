package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	confDir          = "/etc/mysql/conf.d"
	identityFile     = "10-identity.cnf"
	groupReplFile    = "20-gr.cnf"
	defaultMySQLPort = 3306
	serverIDOffset   = 1
)

func main() {
	podName := mustEnv("POD_NAME")
	podNS := mustEnv("POD_NAMESPACE")
	replicas := mustIntEnv("GR_REPLICAS")
	mysqlPort := getIntEnv("MYSQL_PORT", defaultMySQLPort)
	groupName := mustEnv("GR_GROUP_NAME")
	// podIP := mustEnv("POD_IP")
	clusterDomain := getEnv("CLUSTER_DOMAIN", "cluster.local")

	ordinal := parseOrdinal(podName)
	service := deriveService(podName)
	svcFQDN := fmt.Sprintf("%s.%s.svc.%s", service, podNS, clusterDomain)

	reportHost := fmt.Sprintf("%s.%s", podName, svcFQDN)

	seeds := buildSeeds(service, svcFQDN, replicas, mysqlPort)
	localAddr := fmt.Sprintf("%s:%d", reportHost, mysqlPort)

	serverID := ordinal + serverIDOffset

	writeFile(filepath.Join(confDir, identityFile), fmt.Sprintf(`[mysqld]
server-id = %d
report_host = %s
`, serverID, reportHost))

	writeFile(filepath.Join(confDir, groupReplFile), fmt.Sprintf(`[mysqld]
loose-group_replication_local_address = %s
loose-group_replication_group_seeds = %s
loose-group_replication_group_name = %s
loose-group_replication_start_on_boot = off
loose-group_replication_bootstrap_group = off
loose-group_replication_communication_stack = MYSQL
plugin_load_add = 'group_replication.so'
`, localAddr, seeds, groupName))
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fail(fmt.Sprintf("missing env %s", key))
	}
	return v
}

func mustIntEnv(key string) int {
	v := mustEnv(key)
	i, err := strconv.Atoi(v)
	if err != nil {
		fail(fmt.Sprintf("env %s is not int: %v", key, err))
	}
	return i
}

func getIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			fail(fmt.Sprintf("env %s is not int: %v", key, err))
		}
		return i
	}
	return def
}

func parseOrdinal(pod string) int {
	parts := strings.Split(pod, "-")
	if len(parts) == 0 {
		fail("cannot parse ordinal from pod name")
	}
	ordStr := parts[len(parts)-1]
	ord, err := strconv.Atoi(ordStr)
	if err != nil {
		fail(fmt.Sprintf("invalid ordinal %q", ordStr))
	}
	return ord
}

func deriveService(pod string) string {
	i := strings.LastIndex(pod, "-")
	if i <= 0 {
		fail(fmt.Sprintf("cannot derive service from pod name %q", pod))
	}
	return pod[:i]
}

func buildSeeds(service, svcFQDN string, replicas, port int) string {
	var hosts []string
	for i := 0; i < replicas; i++ {
		hosts = append(hosts, fmt.Sprintf("%s-%d.%s:%d", service, i, svcFQDN, port))
	}
	return strings.Join(hosts, ",")
}

func writeFile(path, contents string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail(fmt.Sprintf("mkdir %s: %v", filepath.Dir(path), err))
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		fail(fmt.Sprintf("write %s: %v", path, err))
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
