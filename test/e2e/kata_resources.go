package kata

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"k8s.io/apimachinery/pkg/util/wait"
)

func deleteKataResource(oc *CLI, res, resNs, resName string) bool {
	const pollInterval = 15 * time.Second

	Logf("Initiating deletion of %v %v in ns %v (timeout: %v)", res, resName, resNs, podDeleteTimeout*time.Second)

	msg, err := oc.AsAdmin().WithoutNamespace().Run("delete").Args(
		res, resName, "-n", resNs,
		"--ignore-not-found",
		"--wait=false",
	).Output()

	if err != nil {
		Logf("Delete command returned: %v %v", msg, err)
	}

	startTime := time.Now()
	var lastOutput string

	ctx, cancel := context.WithTimeout(context.Background(), podDeleteTimeout*time.Second)
	defer cancel()
	errCheck := wait.PollUntilContextTimeout(ctx, pollInterval, podDeleteTimeout*time.Second, true, func(_ context.Context) (bool, error) {
		// --ignore-not-found makes a missing resource exit 0 with empty stdout,
		// so an empty result means deleted. Output() returns stdout only, so the
		// "NotFound" message is not available to match on.
		output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			res, resName, "-n", resNs, "--no-headers", "--ignore-not-found",
		).Output()
		lastOutput = output
		if err != nil {
			Logf("get %v %v returned error: %v", res, resName, err)
			return false, nil
		}

		if output == "" {
			return true, nil
		}

		elapsed := time.Since(startTime)
		Logf("Waiting for %v %v deletion... (elapsed: %v, status: %v)",
			res, resName, elapsed.Round(time.Second), output)
		return false, nil
	})

	elapsed := time.Since(startTime)

	if errCheck != nil {
		phase, phaseErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			res, resName, "-n", resNs, "-o=jsonpath={.status.phase}",
		).Output()
		if phaseErr != nil {
			Logf("failed to get phase for %v %v: %v", res, resName, phaseErr)
		}
		conditions, condErr := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			res, resName, "-n", resNs, "-o=jsonpath={.status.conditions}",
		).Output()
		if condErr != nil {
			Logf("failed to get conditions for %v %v: %v", res, resName, condErr)
		}

		ginkgo.Fail(fmt.Sprintf("DELETION TIMEOUT: %v %v in ns %v was not deleted within %v.\n"+
			"  Last status: %v\n"+
			"  Phase: %v\n"+
			"  Conditions: %v\n"+
			"  Deletion will continue in background.",
			res, resName, resNs, podDeleteTimeout*time.Second,
			strings.TrimSpace(lastOutput), phase, conditions))

		return false
	}

	Logf("Successfully deleted %v %v in ns %v in %v", res, resName, resNs, elapsed.Round(time.Second))
	return true
}

func deleteResource(oc *CLI, res, resName, resNs string, duration, interval time.Duration) (string, error) {
	msg, err := oc.AsAdmin().WithoutNamespace().Run("delete").Args(
		res, resName, "-n", resNs,
		"--ignore-not-found",
		"--wait=false",
	).Output()
	if err != nil {
		Failf("Cannot start deleting %v %v -n %v: %v %v", res, resName, resNs, msg, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	errCheck := wait.PollUntilContextTimeout(ctx, interval, duration, true, func(_ context.Context) (bool, error) {
		// See deleteKataResource: an empty result under --ignore-not-found means
		// the resource is gone.
		var getErr error
		msg, getErr = oc.AsAdmin().WithoutNamespace().Run("get").Args(
			res, resName, "-n", resNs, "--no-headers", "--ignore-not-found",
		).Output()
		if getErr != nil {
			Logf("get %v %v returned error: %v", res, resName, getErr)
			return false, nil
		}
		if msg == "" {
			return true, nil
		}
		return false, nil
	})
	if errCheck != nil {
		Logf("Timeout waiting for delete to finish on %v %v -n %v: %v", res, resName, resNs, msg)
	}
	AssertWaitPollNoErr(errCheck, fmt.Sprintf("%v %v was not finally deleted in ns %v", res, resName, resNs))

	return fmt.Sprintf("deleted %v %v -n %v", res, resName, resNs), nil
}

func checkResourceJsonpath(oc *CLI, resType, resName, resNs, jsonpath, expected string, duration, interval time.Duration) (string, error) {
	var msg string
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	errCheck := wait.PollUntilContextTimeout(ctx, interval, duration, true, func(_ context.Context) (bool, error) {
		var err error
		msg, err = oc.AsAdmin().WithoutNamespace().Run("get").Args(resType, resName, "-n", resNs, jsonpath).Output()
		if err != nil {
			Logf("get %v %v returned error: %v", resType, resName, err)
		}
		if strings.Contains(msg, expected) {
			return true, nil
		}
		return false, nil
	})
	AssertWaitPollNoErr(errCheck, fmt.Sprintf("%v %v in ns %v is not in %v state after %v: %v", resType, resName, resNs, expected, duration, msg))
	return msg, nil
}

func checkControlPod(oc *CLI, podName, podNs, expStatus string) (string, error) {
	return checkResourceJsonpath(oc, "pods", podName, podNs, "-o=jsonpath={.status.phase}", expStatus, podSnooze*time.Second, 10*time.Second)
}
