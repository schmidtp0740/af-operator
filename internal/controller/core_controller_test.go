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

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/exp/rand"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	nodev1alpha1 "github.com/schmidtp0740/cardano-operator/api/v1alpha1"
)

var _ = Describe("Core Controller", func() {
	Context("When reconciling a resource", func() {
		var (
			err                  error
			coreResource         *nodev1alpha1.Core
			ctx                  context.Context
			typeNamespacedName   types.NamespacedName
			ns                   *v1.Namespace
			controllerReconciler *CoreReconciler
			hostPath             string
		)
		const resourceName = "test-resource"
		const timeout = time.Second * 5
		const interval = time.Second * 1

		randString := func(n int) string {
			var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz0123456789")
			b := make([]rune, n)
			for i := range b {
				b[i] = letterRunes[rand.Intn(len(letterRunes))]
			}
			return string(b)
		}

		BeforeEach(func() {
			ctx = context.Background()

			By("creating the custom resource for the Kind Core")
			typeNamespacedName = types.NamespacedName{
				Name: resourceName,
				// prefix namespace with "test-core" then apppend with a 5 character random string
				Namespace: "test-core-" + randString(20),
			}

			// create namespace if not exists
			ns = &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: typeNamespacedName.Namespace,
				},
			}

			// Default Storage Class
			hostPath = "hostpath"

			coreResource = &nodev1alpha1.Core{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: nodev1alpha1.CoreSpec{
					NodeSpec: nodev1alpha1.NodeSpec{
						Replicas:         1,
						ImagePullSecrets: []v1.LocalObjectReference{{Name: "ocirsecret"}},
						Image:            "alpine:latest",
						Storage: v1.PersistentVolumeClaimSpec{
							AccessModes:      []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
							StorageClassName: &hostPath,
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{v1.ResourceStorage: resource.MustParse("1Gi")},
							},
						},
						Service: nodev1alpha1.NodeServiceSpec{
							Annotations: map[string]string{
								"serviceAnnotation": "true",
							},
							Type: v1.ServiceTypeClusterIP,
							Port: 31400,
						},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("100m"),
								v1.ResourceMemory: resource.MustParse("128Mi"),
							},
							Limits: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("200m"),
								v1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					},
				},
			}

		})

		JustBeforeEach(func() {

			// Create the namespace
			err = k8sClient.Create(ctx, ns)
			Expect(err).NotTo(HaveOccurred())

			// Verify the Core resource doesnt exist
			err = k8sClient.Get(ctx, typeNamespacedName, coreResource)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue())

			// Create the Core resource
			Expect(k8sClient.Create(ctx, coreResource)).To(Succeed())

			controllerReconciler = &CoreReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

		})

		AfterEach(func() {
			By("Delete the namespace")
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})

		It("should successfully create the core resource", func() {

			By("Reconcile to create the core resource")
			// Keep reconciling until the requeue is false
			// to ensure all resources are created and status is updated
			Eventually(func() bool {
				recon, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				return recon.Requeue

			}, timeout, interval).Should(BeFalse())

			// check core is created
			Eventually(func() *nodev1alpha1.Core {
				f := &nodev1alpha1.Core{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				return f
			}).ShouldNot(BeNil())

			// check statefulset is created
			Eventually(func() string {
				f := &appsv1.StatefulSet{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				return f.Spec.ServiceName
			}, timeout, interval).Should(Equal(typeNamespacedName.Name))

			// check replicas in statefulset
			// is equal to the replicas in the core spec
			// which should be 1
			Eventually(func() int32 {
				f := &appsv1.StatefulSet{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				return *f.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(1)))

			// Verify the statefulset
			statefulSet := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(err).NotTo(HaveOccurred())
			Expect(statefulSet).NotTo(BeNil())

			// Verify the labels of the statefulset
			Expect(len(statefulSet.Labels)).To(Equal(3))
			Expect(statefulSet.Labels).To(HaveKeyWithValue("app", "cardano-node"))
			Expect(statefulSet.Labels).To(HaveKeyWithValue("instance", "core"))
			Expect(statefulSet.Labels).To(HaveKeyWithValue("relay_cr", typeNamespacedName.Name))

			// Verify the serviceName
			Expect(statefulSet.Spec.ServiceName).To(Equal(typeNamespacedName.Name))

			// Verify the selector labels
			Expect(len(statefulSet.Spec.Selector.MatchLabels)).To(Equal(3))
			Expect(statefulSet.Spec.Selector.MatchLabels).To(HaveKeyWithValue("app", "cardano-node"))
			Expect(statefulSet.Spec.Selector.MatchLabels).To(HaveKeyWithValue("instance", "core"))
			Expect(statefulSet.Spec.Selector.MatchLabels).To(HaveKeyWithValue("relay_cr", typeNamespacedName.Name))

			// Verify the replicas
			Expect(*statefulSet.Spec.Replicas).To(Equal(int32(1)))

			// Verify the pod image pull secrets
			Expect(statefulSet.Spec.Template.Spec.ImagePullSecrets).To(HaveLen(1))
			Expect(statefulSet.Spec.Template.Spec.ImagePullSecrets[0].Name).To(Equal("ocirsecret"))

			// Verify the pod template labels
			Expect(len(statefulSet.Spec.Template.Labels)).To(Equal(3))
			Expect(statefulSet.Spec.Template.Labels).To(HaveKeyWithValue("app", "cardano-node"))
			Expect(statefulSet.Spec.Template.Labels).To(HaveKeyWithValue("instance", "core"))
			Expect(statefulSet.Spec.Template.Labels).To(HaveKeyWithValue("relay_cr", typeNamespacedName.Name))

			// Verify the template annotations
			Expect(len(statefulSet.Spec.Template.Annotations)).To(Equal(3))
			Expect(statefulSet.Spec.Template.Annotations).To(HaveKeyWithValue("prometheus.io/scrape", "true"))
			Expect(statefulSet.Spec.Template.Annotations).To(HaveKeyWithValue("prometheus.io/port", "8080"))
			Expect(statefulSet.Spec.Template.Annotations).To(HaveKeyWithValue("prometheus.io/path", "/metrics"))

			// Verify the Template Volumes
			Expect(statefulSet.Spec.Template.Spec.Volumes).To(HaveLen(3))
			Expect(statefulSet.Spec.Template.Spec.Volumes[0].Name).To(Equal("node-ipc"))
			Expect(statefulSet.Spec.Template.Spec.Volumes[1].Name).To(Equal("cardano-config"))
			Expect(statefulSet.Spec.Template.Spec.Volumes[2].Name).To(Equal("nodeop-secrets"))

			// Verify the Container
			Expect(statefulSet.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Name).To(Equal("cardano-node"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Image).To(Equal("alpine:latest"))
			Expect(len(statefulSet.Spec.Template.Spec.Containers[0].Ports)).To(Equal(2))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort).To(Equal(int32(31400)))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Ports[1].ContainerPort).To(Equal(int32(8080)))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Resources.Requests.Cpu().AsDec().String()).To(Equal("0.100"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Resources.Requests.Memory().AsDec().String()).To(Equal("134217728"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().AsDec().String()).To(Equal("0.200"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().AsDec().String()).To(Equal("268435456"))

			// Verify the readiness probe
			Expect(statefulSet.Spec.Template.Spec.Containers[0].ReadinessProbe).NotTo(BeNil())
			Expect(statefulSet.Spec.Template.Spec.Containers[0].ReadinessProbe.TCPSocket.Port.IntVal).To(Equal(int32(31400)))

			// Verify the liveness probe
			Expect(statefulSet.Spec.Template.Spec.Containers[0].LivenessProbe).NotTo(BeNil())
			Expect(statefulSet.Spec.Template.Spec.Containers[0].LivenessProbe.TCPSocket.Port.IntVal).To(Equal(int32(31400)))

			// Verify the args contain the following flags:
			// --shelley-kes-key
			// --shelley-vrf-key
			// --shelley-operational-certificate
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--shelley-kes-key"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--shelley-vrf-key"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--shelley-operational-certificate"))

			// Verify the CARDANO_BLOCK_PRODUCER environment variable
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Env).To(HaveLen(1))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].Env[0].Name).To(Equal("CARDANO_BLOCK_PRODUCER"))

			// Verify the volume mounts
			Expect(statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts).To(HaveLen(4))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts[0].Name).To(Equal("node-db"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts[0].MountPath).To(Equal("/data"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts[1].Name).To(Equal("node-ipc"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts[1].MountPath).To(Equal("/ipc"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts[2].Name).To(Equal("cardano-config"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts[2].MountPath).To(Equal("/configuration"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts[3].Name).To(Equal("nodeop-secrets"))
			Expect(statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts[3].MountPath).To(Equal("/nodeop"))

			// Verify the volumeClaimTemplate
			Expect(statefulSet.Spec.VolumeClaimTemplates).To(HaveLen(1))
			Expect(statefulSet.Spec.VolumeClaimTemplates[0].Name).To(Equal("node-db"))
			Expect(statefulSet.Spec.VolumeClaimTemplates[0].Spec.AccessModes).To(ContainElement(v1.ReadWriteOnce))
			Expect(statefulSet.Spec.VolumeClaimTemplates[0].Spec.StorageClassName).To(Equal(&hostPath))

			// check service is created
			Eventually(func() *v1.Service {
				f := &v1.Service{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				return f
			}).ShouldNot(BeNil())

			// verify service
			service := &v1.Service{}
			err = k8sClient.Get(ctx, typeNamespacedName, service)
			Expect(err).NotTo(HaveOccurred())
			Expect(service).NotTo(BeNil())

			// Verify the service labels
			Expect(len(service.Labels)).To(Equal(3))
			Expect(service.Labels).To(HaveKeyWithValue("app", "cardano-node"))
			Expect(service.Labels).To(HaveKeyWithValue("instance", "core"))
			Expect(service.Labels).To(HaveKeyWithValue("relay_cr", typeNamespacedName.Name))

			// Verify the Annotations
			Expect(len(service.Annotations)).To(Equal(1))
			Expect(service.Annotations).To(HaveKeyWithValue("serviceAnnotation", "true"))

			// Verify the selector labels
			Expect(len(service.Spec.Selector)).To(Equal(3))
			Expect(service.Spec.Selector).To(HaveKeyWithValue("app", "cardano-node"))
			Expect(service.Spec.Selector).To(HaveKeyWithValue("instance", "core"))
			Expect(service.Spec.Selector).To(HaveKeyWithValue("relay_cr", typeNamespacedName.Name))

			// Verify the service type
			Expect(service.Spec.Type).To(Equal(v1.ServiceTypeClusterIP))

			// Verify the service clusterIP
			Expect(service.Spec.ClusterIP).NotTo(BeEmpty())

			// Verify the service ports
			Expect(len(service.Spec.Ports)).To(Equal(1))
			Expect(service.Spec.Ports[0].Port).To(Equal(int32(31400)))
			Expect(service.Spec.Ports[0].TargetPort.IntVal).To(Equal(int32(31400)))

		})

		It("should successfully update the core resource", func() {

			// Reconcile to initially create the resource
			Eventually(func() bool {
				recon, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				return recon.Requeue

			}, timeout, interval).Should(BeFalse())

			// check replicas in statefulset
			// is equal to the replicas in the core spec
			// which should be 1
			Eventually(func() int32 {
				f := &appsv1.StatefulSet{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				return *f.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(1)))

			By("Update core to 2 replicas")
			coreResource.Spec.Replicas = 2
			Expect(k8sClient.Update(context.Background(), coreResource)).Should(Succeed())

			// Keep reconciling until the requeue is false
			Eventually(func() bool {
				recon, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				return recon.Requeue

			}, timeout, interval).Should(BeFalse())

			// check core is updated
			// with new replicas
			Eventually(func() int32 {
				f := &nodev1alpha1.Core{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				return f.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(2)))

			// check statefulset is updated
			// with new replicas
			Eventually(func() int32 {
				f := &appsv1.StatefulSet{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				return *f.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(2)))

			By("Update image")
			newImageName := "ubuntu:latest"
			coreResource.Spec.Image = newImageName
			Expect(k8sClient.Update(context.Background(), coreResource)).Should(Succeed())

			// Keep reconciling until the requeue is false
			Eventually(func() bool {
				recon, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				return recon.Requeue

			}, timeout, interval).Should(BeFalse())

			// check statefulset is updated
			// with new image
			Eventually(func() string {
				f := &appsv1.StatefulSet{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				if len(f.Spec.Template.Spec.Containers) == 1 {
					return f.Spec.Template.Spec.Containers[0].Image
				}
				return ""
			}, timeout, interval).Should(Equal(newImageName))

		})

		It("should successfully delete the core resource", func() {

			// Reconcile to initially create the resource
			Eventually(func() bool {
				recon, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
				return recon.Requeue

			}, timeout, interval).Should(BeFalse())

			// Verify the Core resource exists
			coreResource = &nodev1alpha1.Core{}
			err = k8sClient.Get(ctx, typeNamespacedName, coreResource)
			Expect(err).NotTo(HaveOccurred())
			Expect(coreResource).NotTo(BeNil())

			// Verify the StatefulSet resource exists
			statefulSet := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, typeNamespacedName, statefulSet)
			Expect(err).NotTo(HaveOccurred())
			Expect(statefulSet).NotTo(BeNil())

			By("Expecting to delete successfully")
			Eventually(func() error {
				f := &nodev1alpha1.Core{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				return k8sClient.Delete(context.Background(), f)
			}, timeout, interval).Should(Succeed())

			By("Expecting to delete finish")
			Eventually(func() error {
				f := &nodev1alpha1.Core{}
				return k8sClient.Get(context.Background(), typeNamespacedName, f)
			}, timeout, interval).ShouldNot(Succeed())

			coreResource = &nodev1alpha1.Core{}
			err = k8sClient.Get(ctx, typeNamespacedName, coreResource)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})
})
