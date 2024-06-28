/*
Copyright 2024.

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

package e2e

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/schmidtp0740/cardano-operator/test/utils"
)

const namespace = "cardano-operator-system"

var _ = Describe("controller", Ordered, func() {
	var controllerPodName string
	var err error
	var projectDir string
	var projectimage string

	BeforeAll(func() {

		projectDir, _ = utils.GetProjectDir()
		By("Change the kubeconfig context to the docker-desktop cluster")
		cmd := exec.Command("kubectl", "config", "use-context", "docker-desktop")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("installing prometheus operator")
		Expect(utils.InstallPrometheusOperator()).To(Succeed())

		By("installing the cert-manager")
		Expect(utils.InstallCertManager()).To(Succeed())

		By("creating manager namespace")
		cmd = exec.Command("kubectl", "create", "ns", namespace)
		_, _ = utils.Run(cmd)

		// projectimage stores the name of the image used in the example
		// use random image name to avoid conflicts with other tests
		imageTag := fmt.Sprintf("%d", time.Now().Unix())
		projectimage = "cardano-operator:" + imageTag

		By("building the manager(Operator) image")
		cmd = exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectimage))
		_, err = utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectimage))
		_, err = utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())

		By("validating that the controller-manager pod is running as expected")
		verifyControllerUp := func() error {
			// Get pod name

			cmd = exec.Command("kubectl", "get",
				"pods", "-l", "control-plane=controller-manager",
				"-o", "go-template={{ range .items }}"+
					"{{ if not .metadata.deletionTimestamp }}"+
					"{{ .metadata.name }}"+
					"{{ \"\\n\" }}{{ end }}{{ end }}",
				"-n", namespace,
			)

			podOutput, err := utils.Run(cmd)
			ExpectWithOffset(2, err).NotTo(HaveOccurred())
			podNames := utils.GetNonEmptyLines(string(podOutput))
			if len(podNames) != 1 {
				return fmt.Errorf("expect 1 controller pods running, but got %d", len(podNames))
			}
			controllerPodName = podNames[0]
			ExpectWithOffset(2, controllerPodName).Should(ContainSubstring("controller-manager"))

			// Validate pod status
			cmd = exec.Command("kubectl", "get",
				"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
				"-n", namespace,
			)
			status, err := utils.Run(cmd)
			ExpectWithOffset(2, err).NotTo(HaveOccurred())
			if string(status) != "Running" {
				return fmt.Errorf("controller pod in %s status", status)
			}
			return nil
		}
		EventuallyWithOffset(1, verifyControllerUp, time.Minute, time.Second).Should(Succeed())

	})

	AfterAll(func() {
		By("uninstalling the Prometheus manager bundle")
		utils.UninstallPrometheusOperator()

		By("uninstalling the cert-manager bundle")
		utils.UninstallCertManager()

		By("removing manager namespace")
		cmd := exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	Context("Relay", func() {

		BeforeEach(func() {
			By("creating a Cardano relay resource")
			EventuallyWithOffset(1, func() error {
				cmd := exec.Command("kubectl", "apply", "-f", filepath.Join(projectDir,
					"config/samples/node_v1alpha1_relay.yaml"), "-n", namespace)
				_, err = utils.Run(cmd)
				return err
			}, time.Minute, time.Second).Should(Succeed())
		})

		AfterEach(func() {
			// Delete the relay resource
			cmd := exec.Command("kubectl", "delete", "relay", "cardano-relay", "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("Verify basic deployment", func() {

			It("should deploy successfully", func() {

				By("validating that pod(s) status.phase=Running")
				getRelayPodStatus := func() error {
					cmd := exec.Command("kubectl", "get",
						"pods", "-l", "relay_cr=cardano-relay",
						"-o", "jsonpath={.items[*].status}", "-n", namespace,
					)
					status, err := utils.Run(cmd)
					fmt.Println(string(status))
					ExpectWithOffset(2, err).NotTo(HaveOccurred())
					if !strings.Contains(string(status), "\"phase\":\"Running\"") {
						return fmt.Errorf("cardano relay pod in %s status", status)
					}
					return nil
				}
				EventuallyWithOffset(1, getRelayPodStatus, time.Minute, time.Second).Should(Succeed())

			})
		})

		Context("Verify updating the replicas", func() {

			BeforeEach(func() {
				By("validating that pod(s) status.phase=Running")
				getRelayPodStatus := func() error {
					cmd := exec.Command("kubectl", "get",
						"pods", "-l", "relay_cr=cardano-relay",
						"-o", "jsonpath={.items[*].status.phase}", "-n", namespace,
					)
					status, err := utils.Run(cmd)
					fmt.Println(string(status))
					ExpectWithOffset(2, err).NotTo(HaveOccurred())
					if strings.Count(string(status), "Running") != 1 {
						return fmt.Errorf("cardano relay pod in %s status", status)
					}
					return nil
				}
				EventuallyWithOffset(1, getRelayPodStatus, time.Minute, time.Second).Should(Succeed())

				// Patch the CR to update the replicas
				EventuallyWithOffset(1, func() error {
					cmd := exec.Command("kubectl", "patch", "relay", "cardano-relay",
						"-n", namespace, "--type", "merge", "-p", `{"spec":{"replicas":2}}`)
					_, err = utils.Run(cmd)
					return err
				}, time.Minute, time.Second).Should(Succeed())

			})

			It("should update the replicas successfully", func() {

				// Validate the replicas are updated
				EventuallyWithOffset(1, func() error {
					cmd := exec.Command("kubectl", "get", "relay", "cardano-relay",
						"-n", namespace, "-o", "jsonpath={.spec.replicas}")
					replicas, err := utils.Run(cmd)
					if err != nil {
						return err
					}
					if string(replicas) != "2" {
						return fmt.Errorf("expect replicas=2, but got %s", replicas)
					}
					return nil
				}, time.Minute, time.Second).Should(Succeed())

				// Validate 2 pods to be in running state
				By("validating that pod(s) status.phase=Running")
				getRelayPodStatus := func() error {
					cmd := exec.Command("kubectl", "get",
						"pods", "-l", "relay_cr=cardano-relay",
						"-o", "jsonpath={.items[*].status.phase}", "-n", namespace,
					)
					status, err := utils.Run(cmd)
					fmt.Println(string(status))
					ExpectWithOffset(2, err).NotTo(HaveOccurred())
					// Verify that 'Running' status is returned twice
					if strings.Count(string(status), "Running") != 2 {
						return fmt.Errorf("cardano relay pod in %s status", status)
					}
					return nil
				}
				EventuallyWithOffset(1, getRelayPodStatus, time.Minute, time.Second).Should(Succeed())

			})
		})
	})
})
