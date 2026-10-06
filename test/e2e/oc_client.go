package kata

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// CLI wraps the oc binary with fluent method chaining.
// It serves two roles depending on lifecycle:
//   - As a "client" (created by NewOC): holds kubeconfig path and namespace.
//   - As a "command builder" (created by Run): holds the assembled command arguments.
type CLI struct {
	execPath         string
	configPath       string
	adminConfigPath  string
	namespace        string
	withoutNamespace bool
	globalArgs       []string
	commandArgs      []string
}

// NewOC creates a CLI and registers Ginkgo lifecycle hooks for namespace management.
// Must be called at Describe/Context level, not inside It blocks.
func NewOC(baseName string) *CLI {
	adminConfig := os.Getenv("KUBECONFIG")
	cli := &CLI{
		execPath:        "oc",
		adminConfigPath: adminConfig,
		configPath:      adminConfig,
	}

	ginkgo.BeforeEach(func() {
		nsName := fmt.Sprintf("e2e-%s-%s", baseName, getRandomString())
		cli.namespace = nsName

		_, err := cli.AsAdmin().WithoutNamespace().Run("create").Args("namespace", nsName).Output()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to create namespace %s", nsName)

		ginkgo.DeferCleanup(func() {
			if _, err := cli.AsAdmin().WithoutNamespace().Run("delete").Args(
				"namespace", nsName, "--ignore-not-found", "--wait=false",
			).Output(); err != nil {
				Logf("warning: failed to delete namespace %s: %v", nsName, err)
			}
		})

		err = cli.AsAdmin().WithoutNamespace().Run("label").Args(
			"namespace", nsName,
			"pod-security.kubernetes.io/enforce=privileged",
			"pod-security.kubernetes.io/audit=privileged",
			"pod-security.kubernetes.io/warn=privileged",
			"security.openshift.io/scc.podSecurityLabelSync=false",
			"--overwrite",
		).Execute()
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "failed to label namespace %s", nsName)
	})

	return cli
}

func (c *CLI) AsAdmin() *CLI {
	nc := *c
	nc.configPath = c.adminConfigPath
	return &nc
}

func (c CLI) WithoutNamespace() *CLI {
	c.withoutNamespace = true
	return &c
}

func (c *CLI) Run(commands ...string) *CLI {
	nc := &CLI{
		execPath:        c.execPath,
		adminConfigPath: c.adminConfigPath,
		configPath:      c.configPath,
		namespace:       c.namespace,
		globalArgs:      make([]string, 0, len(commands)+2),
	}
	if c.configPath != "" {
		nc.globalArgs = append(nc.globalArgs, "--kubeconfig="+c.configPath)
	}
	if !c.withoutNamespace && c.namespace != "" {
		nc.globalArgs = append(nc.globalArgs, "--namespace="+c.namespace)
	}
	nc.globalArgs = append(nc.globalArgs, commands...)
	return nc
}

func (c *CLI) Args(args ...string) *CLI {
	c.commandArgs = args
	return c
}

func (c *CLI) Namespace() string {
	return c.namespace
}

func (c *CLI) Output() (string, error) {
	finalArgs := append(c.globalArgs, c.commandArgs...)
	cmd := exec.Command(c.execPath, finalArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	errOut := strings.TrimSpace(stderr.String())

	safeArgs := redactArgs(finalArgs)
	if err != nil {
		if errOut != "" {
			Logf("exec error: %s %s (exit: %v)\n%s", c.execPath, safeArgs, err, errOut)
			return out, fmt.Errorf("%s %s failed: %s: %w", c.execPath, safeArgs, errOut, err)
		}
		Logf("exec error: %s %s (exit: %v)", c.execPath, safeArgs, err)
		return out, fmt.Errorf("%s %s failed: %w", c.execPath, safeArgs, err)
	}
	if errOut != "" {
		Logf("exec stderr: %s %s\n%s", c.execPath, safeArgs, errOut)
	}
	return out, nil
}

var sensitiveFlags = map[string]bool{
	"-p": true, "--patch": true, "--kubeconfig": true, "--from-literal": true,
}

func redactArgs(args []string) string {
	safe := make([]string, len(args))
	skip := false
	for i, a := range args {
		if skip {
			safe[i] = "<redacted>"
			skip = false
			continue
		}
		if strings.Contains(a, "=") {
			key := a[:strings.Index(a, "=")]
			if sensitiveFlags[key] {
				safe[i] = key + "=<redacted>"
				continue
			}
		}
		if sensitiveFlags[a] {
			safe[i] = a
			skip = true
			continue
		}
		safe[i] = a
	}
	return strings.Join(safe, " ")
}

func (c *CLI) Execute() error {
	out, err := c.Output()
	if out != "" {
		_, _ = fmt.Fprintln(ginkgo.GinkgoWriter, out)
	}
	return err
}
