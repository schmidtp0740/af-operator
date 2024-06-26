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
			coreResource       *nodev1alpha1.Core
			ctx                context.Context
			typeNamespacedName types.NamespacedName
			ns                 *v1.Namespace
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
				Namespace: "test-core-" + randString(5),
			}

			// create namespace if not exists
			ns = &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: typeNamespacedName.Namespace,
				},
			}
			err := k8sClient.Create(ctx, ns)
			Expect(err).NotTo(HaveOccurred())

		})

		AfterEach(func() {
			By("Delete the namespace")
			Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {

			hostPath := "hostpath"

			err := k8sClient.Get(ctx, typeNamespacedName, coreResource)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeFalse())

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
								Requests: v1.ResourceList{v1.ResourceName(v1.ResourceStorage): resource.MustParse("1Gi")},
							},
						},
						Service: nodev1alpha1.NodeServiceSpec{
							Type: v1.ServiceTypeClusterIP,
							Port: 31400,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, coreResource)).To(Succeed())

			By("Reconcile to create the statefulset")
			controllerReconciler := &CoreReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

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

			// check service is created
			Eventually(func() *v1.Service {
				f := &v1.Service{}
				_ = k8sClient.Get(context.Background(), typeNamespacedName, f)
				return f
			}).ShouldNot(BeNil())

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

			By("Update core to image")
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
