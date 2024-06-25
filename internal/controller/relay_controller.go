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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	nodev1alpha1 "github.com/schmidtp0740/af-operator/api/v1alpha1"
)

// RelayReconciler reconciles a Relay object
type RelayReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=node.apexfusion.com,resources=relays,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=node.apexfusion.com,resources=relays/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=node.apexfusion.com,resources=relays/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;update;patch
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Relay object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *RelayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("relay", req.NamespacedName)

	// Fetch the Relay instance
	relay := &nodev1alpha1.Relay{}
	err := r.Get(ctx, req.NamespacedName, relay)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			logger.Info("Relay resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get Relay")
		return ctrl.Result{}, err
	}

	// Check if the statefulset already exists, if not create a new one
	found := &appsv1.StatefulSet{}
	err = r.Get(ctx, types.NamespacedName{Name: relay.Name, Namespace: relay.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new statefulset
		dep := r.statefulsetForRelay(relay)
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

	// Check if the service already exists, if not create a new one
	foundSvc := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: relay.Name, Namespace: relay.Namespace}, foundSvc)
	if err != nil && errors.IsNotFound(err) {
		// Define a new service
		svc := r.serviceForRelay(relay)
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

	result, err := ensureSpec(relay.Spec.Replicas, found, relay.Spec.NodeSpec, r.Client)
	if err != nil || result.Requeue {
		if err != nil {
			logger.Error(err, "Failed to update StatefulSet", "StatefulSet.Namespace", found.Namespace, "StatefulSet.Name", found.Name)
		}
		return result, err
	}

	result, err = updateStatus(relay.Name, relay.Namespace, labelsForRelay(relay.Name), relay.Status.Nodes, r.Client, func(pods []string) (ctrl.Result, error) {
		relay.Status.Nodes = pods
		err := r.Status().Update(ctx, relay)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	})
	if err != nil || result.Requeue {
		if err != nil {
			logger.Error(err, "Failed to update status", "Relay.Namespace", found.Namespace, "Relay.Name", found.Name)
		}
		return result, err
	}

	return ctrl.Result{}, nil
}

// serviceForRelay returns a Relay Service object
func (r *RelayReconciler) serviceForRelay(relay *nodev1alpha1.Relay) *corev1.Service {
	ls := labelsForRelay(relay.Name)

	svc := generateNodeService(relay.Name, relay.Namespace, ls, relay.Spec.Service)

	// Set Relay instance as the owner and controller
	ctrl.SetControllerReference(relay, svc, r.Scheme)
	return svc
}

// statefulsetForRelay returns a Relay StatefulSet object
func (r *RelayReconciler) statefulsetForRelay(relay *nodev1alpha1.Relay) *appsv1.StatefulSet {
	ls := labelsForRelay(relay.Name)

	state := generateNodeStatefulset(relay.Name,
		relay.Namespace,
		ls,
		relay.Spec.NodeSpec,
		false,
	)

	// Set Relay instance as the owner and controller
	ctrl.SetControllerReference(relay, state, r.Scheme)
	return state
}

// labelsForRelay returns the labels for selecting the resources
// belonging to the given memcached CR name.
func labelsForRelay(name string) map[string]string {
	return map[string]string{
		"app":      "cardano-node",
		"relay_cr": name,
		"instance": "relay",
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *RelayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&nodev1alpha1.Relay{}).
		Complete(r)
}
