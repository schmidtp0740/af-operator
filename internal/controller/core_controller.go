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
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	nodev1alpha1 "github.com/schmidtp0740/cardano-operator/api/v1alpha1"
)

// CoreReconciler reconciles a Core object
type CoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=node.cardano.io,resources=cores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=node.cardano.io,resources=cores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=node.cardano.io,resources=cores/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;update;patch
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Core object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *CoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	events := []string{}

	logger := log.FromContext(ctx).WithValues("core", req.NamespacedName)

	core := &nodev1alpha1.Core{}
	err := r.Get(ctx, req.NamespacedName, core)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			logger.Info("Core resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get Core")
		return ctrl.Result{}, err
	}

	// Check if topology configmap already exists, if not create a new one
	topologyConfigMap, err := createTopologyConfigMap(fmt.Sprintf("%s-%s", "core", core.Name), core.Namespace, core.Spec.Protocol, core.Spec.Network, core.Spec.LocalPeers, false)
	if err != nil {
		logger.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", topologyConfigMap.Namespace, "ConfigMap.Name", topologyConfigMap.Name)
		return ctrl.Result{}, err
	}
	err = r.Get(ctx, types.NamespacedName{Name: topologyConfigMap.Name, Namespace: core.Namespace}, topologyConfigMap)
	if err != nil && errors.IsNotFound(err) {
		topologyConfigMap, err := createTopologyConfigMap(fmt.Sprintf("%s-%s", "core", core.Name), core.Namespace, core.Spec.Protocol, core.Spec.Network, core.Spec.LocalPeers, false)
		if err != nil {
			logger.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", topologyConfigMap.Namespace, "ConfigMap.Name", topologyConfigMap.Name)
			return ctrl.Result{}, err
		}
		logger.Info("Creating a new ConfigMap", "ConfigMap.Namespace", topologyConfigMap.Namespace, "ConfigMap.Name", topologyConfigMap.Name)
		err = r.Create(ctx, topologyConfigMap)
		if err != nil {
			logger.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", topologyConfigMap.Namespace, "ConfigMap.Name", topologyConfigMap.Name)
			return ctrl.Result{}, err
		}
		// ConfigMap created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil

	} else if err != nil {
		logger.Error(err, "Failed to get ConfigMap")
		return ctrl.Result{}, err
	}

	// TODO ensure that configmap is as exected

	// Check if the statefulset already exists, if not create a new one
	found := &appsv1.StatefulSet{}
	err = r.Get(ctx, types.NamespacedName{Name: core.Name, Namespace: core.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {

		// Define a new statefulset
		dep, err := r.statefulsetForCore(core, topologyConfigMap)
		if err != nil {
			logger.Error(err, "Failed to create new StatefulSet", "StatefulSet.Namespace", dep.Namespace, "StatefulSet.Name", dep.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		logger.Info("Creating a new Statefuleset", "StatefulSet.Namespace", dep.Namespace, "StatefulSet.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			logger.Error(err, "Failed to create new StatefulSet", "StatefulSet.Namespace", dep.Namespace, "StatefulSet.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// StatefulSet created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		logger.Error(err, "Failed to get StatefulSet")
		return ctrl.Result{}, err
	}

	// TODO update to kube 1.27 spec
	// Check if the PodDisruptionBudget already exists, if not create a new one
	// foundPDB := &policyv1.PodDisruptionBudget{}
	// err = r.Get(ctx, types.NamespacedName{Name: core.Name, Namespace: core.Namespace}, foundPDB)
	// if err != nil && errors.IsNotFound(err) {
	// 	// Define a new PodDisruptionPolicy
	// 	pdb := r.pdbForCore(core)
	// 	if pdb != nil {
	// 		logger.Info("Creating a new PodDisruptionPolicy", "PodDisruptionPolicy.Namespace", pdb.Namespace, "PodDisruptionPolicy.Name", pdb.Name)
	// 		err = r.Create(ctx, pdb)
	// 		if err != nil {
	// 			logger.Error(err, "Failed to create new PodDisruptionPolicy", "PodDisruptionPolicy.Namespace", pdb.Namespace, "PodDisruptionPolicy.Name", pdb.Name)
	// 			return ctrl.Result{}, err
	// 		}

	// 		return ctrl.Result{Requeue: true}, nil
	// 	}
	// } else if err != nil {
	// 	logger.Error(err, "Failed to get PodDisruptionPolicy")
	// 	return ctrl.Result{}, err
	// }

	// Check if PodDisruptionBudget should be removed
	// if minav, err := intstr.GetValueFromIntOrPercent(foundPDB.Spec.MinAvailable, 1, false); err == nil {
	// 	if int32(minav) >= core.Spec.Replicas {
	// 		// Need to delete PDB
	// 		r.Delete(ctx, foundPDB)
	// 	}
	// }

	// Check if the service already exists, if not create a new one
	foundSvc := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: core.Name, Namespace: core.Namespace}, foundSvc)
	if err != nil && errors.IsNotFound(err) {
		// Define a new service
		svc, err := r.serviceForCore(core)
		if err != nil {
			logger.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return ctrl.Result{Requeue: true}, err
		}
		logger.Info("Creating a new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		err = r.Create(ctx, svc)
		if err != nil {
			logger.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return ctrl.Result{}, err
		}
		// Service created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		logger.Error(err, "Failed to get Service")
		return ctrl.Result{}, err
	}

	logger.Info("Ensuring StatefulSet")
	result, ev, err := ensureStatefulsetSpec(core.Spec.Replicas, found, core.Spec.NodeSpec, r.Client)
	if err != nil || result.Requeue {

		if err != nil {
			logger.Error(err, "Failed to update StatefulSet", "StatefulSet.Namespace", found.Namespace, "StatefulSet.Name", found.Name)
		}
		return result, err
	}

	events = append(events, ev...)

	err = updateStatus(events, &core.Status, core.Namespace, core.Name, r.Client, ctx)
	if err != nil {
		if err != nil {
			logger.Error(err, "Failed to update status", "Core.Namespace", core.Namespace, "Core.Name", core.Name)
		}
	}

	// update status
	err = r.Client.Status().Update(ctx, core)
	if err != nil {
		logger.Error(err, "Failed to update Core status")
	}

	return result, nil
}

// serviceForCore returns a Relay Service object
func (r *CoreReconciler) serviceForCore(core *nodev1alpha1.Core) (*corev1.Service, error) {
	ls := labelsForCore(core.Name)

	svc := generateNodeService(core.Name, core.Namespace, ls, core.Spec.Service)

	// Set Core instance as the owner and controller
	return svc, ctrl.SetControllerReference(core, svc, r.Scheme)
}

func (r *CoreReconciler) statefulsetForCore(core *nodev1alpha1.Core, topologyConfigMap *corev1.ConfigMap) (*appsv1.StatefulSet, error) {
	ls := labelsForCore(core.Name)

	topologyConfigLocalObjectReference := corev1.LocalObjectReference{Name: topologyConfigMap.Name}
	state := generateNodeStatefulset(core.Name,
		core.Namespace,
		ls,
		core.Spec.NodeSpec,
		topologyConfigLocalObjectReference,
		core.Spec.NodeOpSecretVolume,
	)

	// Set Relay instance as the owner and controller

	return state, ctrl.SetControllerReference(core, state, r.Scheme)
}

// labelsForCore returns the labels for selecting the resources
// belonging to the given memcached CR name.
func labelsForCore(name string) map[string]string {
	return map[string]string{
		"app":      "cardano-node",
		"relay_cr": name,
		"instance": "core",
	}
}

func (r *CoreReconciler) ActiveStandbyWatch() {
	ctx := context.Background()
	logger := log.FromContext(ctx).WithValues("core-activestandby", "")

	// your logic here

	for {
		time.Sleep(1 * time.Second)

		// get list of core
		coreList := &nodev1alpha1.CoreList{}
		err := r.List(ctx, coreList)
		if err != nil {
			logger.Error(err, "Unable to get core list")
			continue
		}

		for _, core := range coreList.Items {
			result, err := ensureActiveStandby(core.Name, core.Namespace, labelsForCore(core.Name), r.Client)
			if err != nil || result.Requeue {
				if err != nil {
					logger.Error(err, "Failed to ensure active/standby", "Core.Namespace", core.Namespace, "Core.Name", core.Name)
				}
				continue
			}
		}

	}

}

// func (r *CoreReconciler) pdbForCore(core *nodev1alpha1.Core) *policyv1.PodDisruptionBudget {
// 	ls := labelsForCore(core.Name)

// 	if core.Spec.Replicas <= 1 {
// 		return nil

// 	}

// 	minAvailable := intstr.FromInt(1)

// 	pdb := &policyv1.PodDisruptionBudget{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:      core.Name,
// 			Namespace: core.Namespace,
// 		},
// 		Spec: policyv1.PodDisruptionBudgetSpec{
// 			MinAvailable: &minAvailable,
// 			Selector: &metav1.LabelSelector{
// 				MatchLabels: ls,
// 			},
// 		},
// 	}

// 	// Set Relay instance as the owner and controller
// 	ctrl.SetControllerReference(core, pdb, r.Scheme)
// 	return pdb
// }

// SetupWithManager sets up the controller with the Manager.
func (r *CoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&nodev1alpha1.Core{}).
		Complete(r)
}
