/*
Copyright 2023 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e_test

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func (e *e2e) testCaseBaseProfileOCI([]string) {
	e.seccompOnlyTestCase()

	baseProfileName := "oci://ghcr.io/security-profiles/"

	if clusterType == clusterTypeVanilla && e.containerRuntime != containerRuntimeDocker {
		baseProfileName += strings.ReplaceAll(baseProfileNameCrun, "-", ":")
	} else {
		baseProfileName += strings.ReplaceAll(baseProfileNameRunc, "-", ":")
	}

	namespace := e.getCurrentContextNamespace(defaultNamespace)
	helloProfile := fmt.Sprintf(`
apiVersion: security-profiles-operator.x-k8s.io/v1beta1
kind: SeccompProfile
metadata:
  name: hello
  namespace: %s
spec:
  defaultAction: SCMP_ACT_ERRNO
  baseProfileName: %s
  syscalls:
  - action: SCMP_ACT_ALLOW
    names:
    - arch_prctl
    - set_tid_address
    - exit_group
`, namespace, baseProfileName)

	helloPod := fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: hello
  namespace: %s
spec:
  containers:
  - image: quay.io/security-profiles-operator/test-hello-world:latest
    name: hello
  securityContext:
    seccompProfile:
      type: Localhost
      localhostProfile: operator/%s/hello.json
  restartPolicy: OnFailure
`, namespace, namespace)

	e.logf("Creating hello profile")
	helloProfileFile, err := os.CreateTemp("", "hello-profile*.yaml")
	e.Nil(err)
	defer os.Remove(helloProfileFile.Name())

	_, err = helloProfileFile.WriteString(helloProfile)
	e.Nil(err)
	err = helloProfileFile.Close()
	e.Nil(err)
	e.kubectl("create", "-f", helloProfileFile.Name())
	defer e.kubectl("delete", "-f", helloProfileFile.Name())

	e.logf("Waiting for profile to be reconciled")
	e.waitForProfile("hello")

	e.logf("Creating hello-world pod")
	helloPodFile, err := os.CreateTemp("", "hello-pod*.yaml")
	e.Nil(err)
	defer os.Remove(helloPodFile.Name())

	_, err = helloPodFile.WriteString(helloPod)
	e.Nil(err)
	err = helloPodFile.Close()
	e.Nil(err)
	e.kubectl("create", "-f", helloPodFile.Name())
	defer e.kubectl("delete", "pod", "hello")

	e.logf("Waiting for test pod to be initialized")
	e.waitFor("condition=initialized", "pod", "hello")

	e.logf("Waiting for pod to be completed")
	for i := 0; i < 20; i++ {
		output := e.kubectl("get", "pod", "hello")
		if strings.Contains(output, "Completed") {
			break
		}
		if strings.Contains(output, "CreateContainerError") {
			output := e.kubectl("describe", "pod", "hello")
			e.FailNowf("Unable to create container", output)
		}
		time.Sleep(time.Second)
	}

	e.logf("Testing that container ran successfully")
	output := e.kubectl("logs", "hello")
	e.Contains(output, "Hello from Docker!")
}
