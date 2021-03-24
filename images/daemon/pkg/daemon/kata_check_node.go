
package daemon

import (
    "io/ioutil"
    "strings"
    "regexp"
    "fmt"
    "path/filepath"
    "os"
    "os/exec"
)

const (
	procCPUInfo  = "/proc/cpuinfo"
	sysModuleDir = "/sys/module"
	moduleParamDir = "parameters"
	modProbeCmd  = "modprobe"

    cpuFlagsTag    = "flags"      // nolint: varcheck, unused, deadcode
    cpuVendorField = "vendor_id"  // nolint: varcheck, unused, deadcode
    cpuModelField  = "model name" // nolint: varcheck, unused, deadcode
)

var (
    requiredCPUAttribs = []string {"GenuineIntel"}

    requiredCPUFlags = []string {"vmx", "lm", "sse4_1"}

    requiredKernelModules = map[string]map[string]string {
        "kvm": {},
        "kvm_intel": {
            "unrestricted_guest": "Y",
        },
        "vhost": {},
        "vhost_net": {},
    }
)


func getFileContents(file string) (string, error) {
    bytes, err := ioutil.ReadFile(file)
    if err != nil {
        return "", err
    }

    return string(bytes), nil
}

func findAnchoredString(haystack string, needle string) bool {
    if haystack == "" || needle == "" {
        return false
    }
    pattern := regexp.MustCompile(`\b` + needle + `\b`)
    return pattern.MatchString(haystack)
}

func checkCPU(cpuinfo string, attribs []string) bool {
    for _, attrib := range attribs {
        found := findAnchoredString(cpuinfo, attrib)
        if !found {
            return false
        }
    }
    return true
}

func haveKernelModule(moduleName string) bool {
    path := filepath.Join(sysModuleDir, moduleName)

    // check if module is loaded already
    if _, err := os.Stat(path); err == nil {
        return true
    }

    // try loading the module (as kata-daemon we're root by definition
    // so permissions shouldn't be a concern)
    cmd := exec.Command("modprobe", moduleName)
    if output, err := cmd.CombinedOutput(); err != nil {
        fmt.Printf("'modprobe %s' failed: %s", moduleName, output)
        return false
    }
    return true
}

func getKernelModuleParameterValue(moduleName string, parameterName string) (string, error) {
    path := filepath.Join(sysModuleDir, moduleName, moduleParamDir, parameterName)
    value, err := getFileContents(path)
    if err != nil {
        return "", err
    }

    return strings.TrimRight(value, "\n\r"), nil
}

func checkKernelModules(modules map[string]map[string]string) bool {
    for moduleName, params := range modules {
        fmt.Printf("module: %s\n", moduleName)

        if !haveKernelModule(moduleName) {
            fmt.Printf("  %s: not found\n", moduleName)
            return false
        }

        for param, expectedValue := range params {
            fmt.Printf("  %s: %s\n", param, expectedValue)
            actualValue, err := getKernelModuleParameterValue(moduleName, param)
            if err != nil {
                fmt.Printf("  %s: couldn't get value of parameter %s\n", moduleName, param)
                return false
            }
            if actualValue != expectedValue {
                fmt.Printf("  %s: actual and expected value of parameter %s don't match (%s != %s)\n", moduleName, param, actualValue, expectedValue)
                return false
            }
        }
    }
    return true
}

func getCPUFlags(cpuinfo string) string {
	for _, line := range strings.Split(cpuinfo, "\n") {
		if strings.HasPrefix(line, cpuFlagsTag) {
			fields := strings.Split(line, ":")
			if len(fields) == 2 {
				return strings.TrimSpace(fields[1])
			}
		}
	}

	return ""
}

func nodeCanRunKata() bool {
    cpuinfo, err := getFileContents(procCPUInfo)
    if err != nil {
        fmt.Printf("cannot read file %s", procCPUInfo)
        return false
    }

    cpuFlags := getCPUFlags(cpuinfo)
    if cpuFlags == "" {
        fmt.Printf("cannot find %s in %s", cpuFlagsTag, procCPUInfo)
        return false
    }

    success := checkCPU(cpuinfo, requiredCPUAttribs)
    if !success {
        println("(Some) CPU attribs not found")
        return false
    }

    success = checkCPU(cpuFlags, requiredCPUFlags)
    if !success {
        println("(Some) CPU flags not found")
        return false
    }

    success = checkKernelModules(requiredKernelModules)
    if !success {
        println("(Some) kernel modules not OK")
        return false
    }

    println("All good")

    return true
}


/*
func main () {
    canRunKata := nodeCanRunKata()
    if canRunKata {
        println("node can run kata")
    } else {
        println("node can not run kata")
    }
}
*/
