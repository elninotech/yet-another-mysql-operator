package configmap

import (
	"strings"
	"testing"

	databasev1alpha1 "github.com/elninotech/mysql-operator/api/v1alpha1"
	"github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestRenderMySQLConfigUsesDefaultsWhenBaseIsEmpty(t *testing.T) {
	g := gomega.NewWithT(t)

	cfg := RenderMySQLConfig(nil, nil)

	lines := strings.Split(strings.TrimSuffix(cfg, "\n"), "\n")
	expectedLines := len(defaultMyCnf) + len(defaultMyCnfBooleanFlags) + 2 // header + include dir

	g.Expect(lines).To(gomega.HaveLen(expectedLines))
	g.Expect(lines[0]).To(gomega.Equal("[mysqld]"))
	g.Expect(lines[len(lines)-1]).To(gomega.Equal("!includedir /etc/mysql/conf.d"))
	g.Expect(cfg).To(gomega.ContainSubstring("innodb-flush-method = O_DIRECT"))
	g.Expect(cfg).To(gomega.ContainSubstring("skip-name-resolve\n"))
}

func TestRenderMySQLConfigRespectsProvidedBase(t *testing.T) {
	g := gomega.NewWithT(t)

	base := map[string]string{
		"custom-setting": "enabled",
		"sql-mode":       "ANSI",
	}

	cfg := RenderMySQLConfig(base, nil)

	lines := strings.Split(strings.TrimSuffix(cfg, "\n"), "\n")
	expectedLines := len(base) + len(defaultMyCnfBooleanFlags) + 2

	g.Expect(lines).To(gomega.HaveLen(expectedLines))
	g.Expect(cfg).To(gomega.ContainSubstring("custom-setting = enabled"))
	g.Expect(cfg).To(gomega.ContainSubstring("sql-mode = ANSI"))
	g.Expect(cfg).To(gomega.ContainSubstring("skip-name-resolve\n"))
	g.Expect(cfg).NotTo(gomega.ContainSubstring("binlog_expire_logs_seconds"))
}

func TestRenderMySQLConfigAppliesOverridesOnBase(t *testing.T) {
	g := gomega.NewWithT(t)

	base := make(map[string]string, len(defaultMyCnf))
	for k, v := range defaultMyCnf {
		base[k] = v
	}

	overrides := databasev1alpha1.MysqlConf{
		"log-bin":           intstr.FromString("/custom/binlog"),
		"max-connections":   intstr.FromInt(123),
		"skip-name-resolve": intstr.FromString("off"),
	}

	cfg := RenderMySQLConfig(base, overrides)

	g.Expect(cfg).To(gomega.ContainSubstring("log-bin = /custom/binlog"))
	g.Expect(cfg).To(gomega.ContainSubstring("max-connections = 123"))
	g.Expect(cfg).To(gomega.ContainSubstring("skip-name-resolve = off"))
	g.Expect(cfg).To(gomega.ContainSubstring("innodb-flush-method = O_DIRECT"))
}

func TestRenderMySQLConfigSortsKeys(t *testing.T) {
	g := gomega.NewWithT(t)

	base := map[string]string{"zeta": "last", "alpha": "first"}
	overrides := databasev1alpha1.MysqlConf{
		"beta": intstr.FromString("middle"),
	}

	cfg := RenderMySQLConfig(base, overrides)
	lines := strings.Split(strings.TrimSuffix(cfg, "\n"), "\n")

	g.Expect(lines[1]).To(gomega.Equal("alpha = first"))
	g.Expect(lines[2]).To(gomega.Equal("beta = middle"))
	g.Expect(lines[3]).To(gomega.Equal("skip-name-resolve"))
	g.Expect(lines[4]).To(gomega.Equal("zeta = last"))
	g.Expect(lines[len(lines)-1]).To(gomega.Equal("!includedir /etc/mysql/conf.d"))
}
