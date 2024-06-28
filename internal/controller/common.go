package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	nodev1alpha1 "github.com/schmidtp0740/cardano-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	serviceModeAnnotation = "cardano.io/mode"
	podDesignationLabel   = "cardano.io/designation"
	defaultCardanoPort    = 31400
	defaultPrometheusPort = 8080
)

func generateNodeStatefulset(name string,
	namespace string,
	labels map[string]string,
	nodeSpec nodev1alpha1.NodeSpec,
	topologyConfig corev1.LocalObjectReference,
	nodeOpSecretVolume *corev1.Volume) *appsv1.StatefulSet {

	coreNode := false
	if nodeOpSecretVolume != nil {
		coreNode = true
	}

	state := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
	}

	state.Spec.ServiceName = name

	state.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: labels,
	}

	state.Spec.Replicas = &nodeSpec.Replicas

	state.Spec.Template.Spec.ImagePullSecrets = nodeSpec.ImagePullSecrets

	state.Spec.Template.ObjectMeta.Labels = labels
	state.Spec.Template.ObjectMeta.Annotations = map[string]string{
		"prometheus.io/scrape": "true",
		"prometheus.io/path":   "/metrics",
		"prometheus.io/port":   fmt.Sprintf("%d", defaultPrometheusPort),
	}

	// add container volumes like node-ipc and cardano-config
	state.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: "node-ipc",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: nil,
			},
		},
		{
			Name: "cardano-config",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{
						{
							ConfigMap: &corev1.ConfigMapProjection{
								LocalObjectReference: nodeSpec.ConfigurationConfig,
							},
						},
						{
							ConfigMap: &corev1.ConfigMapProjection{
								LocalObjectReference: topologyConfig,
							},
						},
						{
							ConfigMap: &corev1.ConfigMapProjection{
								LocalObjectReference: nodeSpec.GenesisConfig,
							},
						},
					},
				},
			},
		},
	}

	// Create pod container details
	cardanoNode := corev1.Container{}

	cardanoNode.Name = "cardano-node"
	cardanoNode.Image = nodeSpec.Image
	cardanoNode.Ports = []corev1.ContainerPort{
		{
			ContainerPort: defaultCardanoPort,
			Protocol:      corev1.ProtocolTCP,
			Name:          "cardano",
		},
		{
			ContainerPort: defaultPrometheusPort,
			Protocol:      corev1.ProtocolTCP,
			Name:          "prometheus",
		},
	}

	cardanoNode.Resources = nodeSpec.Resources

	probe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromInt(defaultCardanoPort),
			},
		},
		InitialDelaySeconds: 15,
		PeriodSeconds:       10,
		FailureThreshold:    1500,
	}

	// Set readiness probe
	cardanoNode.ReadinessProbe = probe

	// set livenessProbe
	cardanoNode.LivenessProbe = probe

	cardanoNode.Args = []string{
		"run",
		"+RTS", "-N", "-RTS",
		"--config", "/configuration/configuration.yaml",
		"--database-path", "/data/db",
		"--host-addr", "0.0.0.0",
		"--port", fmt.Sprintf("%d", defaultCardanoPort),
		"--socket-path", "/ipc/node.socket",
		"--topology", "/configuration/topology.json",
	}

	if coreNode {
		cardanoNode.Args = append(cardanoNode.Args,
			"--shelley-kes-key", "/nodeop/hot.skey",
			"--shelley-vrf-key", "/nodeop/vrf.skey",
			"--shelley-operational-certificate", "/nodeop/op.cert",
		)
		cardanoNode.Env = append(cardanoNode.Env, corev1.EnvVar{Name: "CARDANO_BLOCK_PRODUCER", Value: "true"})
	}
	cardanoNode.VolumeMounts = []corev1.VolumeMount{
		{
			Name:      "node-db",
			MountPath: "/data",
		},
		{
			Name:      "node-ipc",
			MountPath: "/ipc",
		},
		{
			Name:      "cardano-config",
			MountPath: "/configuration",
		},
	}

	if coreNode {
		cardanoNode.VolumeMounts = append(cardanoNode.VolumeMounts, corev1.VolumeMount{Name: "nodeop-secrets", MountPath: "/nodeop"})
	}

	state.Spec.Template.Spec.Containers = append(state.Spec.Template.Spec.Containers, cardanoNode)

	// add initContainers
	// if coreNode is true, add initContainers for copy all files from secrets volume to nodeop-secrets volume
	// then modify the permissions of the files
	// to 400
	if coreNode {
		state.Spec.Template.Spec.InitContainers = []corev1.Container{
			{
				Name:  "copy-secrets",
				Image: "alpine:3.12",
				Command: []string{
					"sh",
					"-c",
					"cp /secrets/* /nodeop/ && chmod 400 /nodeop/*",
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "secrets",
						MountPath: "/secrets",
					},
					{
						Name:      "nodeop-secrets",
						MountPath: "/nodeop",
					},
				},
			},
		}
	}

	if coreNode {
		nodeOpSecretVolume.Name = "secrets"
		state.Spec.Template.Spec.Volumes = append(state.Spec.Template.Spec.Volumes, *nodeOpSecretVolume)

		// add a volume with name nodeop-secrets that is an emptyDir
		state.Spec.Template.Spec.Volumes = append(state.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "nodeop-secrets",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	// add volumeClaimTemplate
	state.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-db",
			},
			Spec: nodeSpec.Storage,
		},
	}

	return state
}

func generateNodeService(name string,
	namespace string,
	labels map[string]string,
	service nodev1alpha1.NodeServiceSpec) *corev1.Service {

	svc := &corev1.Service{}

	svc.ObjectMeta = metav1.ObjectMeta{
		Name:        name,
		Namespace:   namespace,
		Annotations: service.Annotations,
		Labels:      labels,
	}

	svc.Spec.Selector = labels

	if service.Type != corev1.ServiceTypeLoadBalancer && service.Type != corev1.ServiceTypeNodePort {
		svc.Spec.ClusterIP = "None"
	}

	svc.Spec.Type = service.Type

	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "cardano",
			Port:       service.Port,
			TargetPort: intstr.FromInt(31400),
			Protocol:   corev1.ProtocolTCP,
		},
	}

	return svc
}

func ensureStatefulsetSpec(replicas int32, found *appsv1.StatefulSet, nodeSpec nodev1alpha1.NodeSpec, r client.Client) (ctrl.Result, []string, error) {

	ctx := context.Background()

	// Ensure the statefulset size is the same as the spec
	if *found.Spec.Replicas != replicas {
		*found.Spec.Replicas = replicas
		err := r.Update(ctx, found)
		if err != nil {
			return ctrl.Result{}, nil, err
		}
		// Spec updated - return and requeue
		return ctrl.Result{Requeue: true}, []string{"Replica updated"}, nil
	}

	// Ensure the statefulset image is the same as the spec
	for key, container := range found.Spec.Template.Spec.Containers {
		if strings.EqualFold(container.Name, "cardano-node") && container.Image != nodeSpec.Image {
			// TODO if the container image switches from inputoutput/cardano-node to another image
			// then it should update the inputoutput special configurations like Initcontainers and volumes
			found.Spec.Template.Spec.Containers[key].Image = nodeSpec.Image
			found.Spec.Template.Spec.ImagePullSecrets = nodeSpec.ImagePullSecrets
			err := r.Update(ctx, found)
			if err != nil {
				return ctrl.Result{}, nil, err
			}
			// Spec updated - return and requeue
			return ctrl.Result{Requeue: true}, []string{"Image updated"}, nil
		}
	}

	// ensure the statefulset resources are the same as the spec
	for key, container := range found.Spec.Template.Spec.Containers {
		if strings.EqualFold(container.Name, "cardano-node") && !reflect.DeepEqual(container.Resources, nodeSpec.Resources) {
			found.Spec.Template.Spec.Containers[key].Resources = nodeSpec.Resources
			err := r.Update(ctx, found)
			if err != nil {
				return ctrl.Result{}, nil, err
			}
			// Spec updated - return and requeue
			return ctrl.Result{Requeue: true}, []string{"Resources updated"}, nil
		}
	}

	return ctrl.Result{}, nil, nil
}

func getPodNames(namespace string, labels map[string]string, r client.Client) ([]string, error) {

	ctx := context.Background()

	// Update the Relay status with the pod names
	// List the pods for this relay's statefulset
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels(labels),
	}
	if err := r.List(ctx, podList, listOpts...); err != nil {
		return nil, err
	}

	pods := podList.Items

	if len(pods) == 0 {
		return nil, nil
	}

	podNames := []string{}
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}

	return podNames, nil
}

func ensureActiveStandby(name string, namespace string, labels map[string]string, r client.Client) (ctrl.Result, error) {
	ctx := context.Background()

	// get all svc's selectors
	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, svc)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get service: %s", err.Error())
	}

	// get eligible pods
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels(labels),
	}
	if err := r.List(ctx, podList, listOpts...); err != nil {
		return ctrl.Result{Requeue: true}, err
	}
	// filter by runnning pods
	eligiblePods := []corev1.Pod{}
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			ready := true
			for _, container := range pod.Status.ContainerStatuses {
				if !container.Ready {
					ready = false
					break
				}
			}

			// if pod's containers are not ready, pod is not ready
			if !ready {
				continue
			}

			// TODO check if cardano container is sync

			// pod is ready and all containers are ready, add to eligibleList
			eligiblePods = append(eligiblePods, pod)
		}
	}

	// if there is not an active pod, promote one
	foundPodDesignated := false
	for _, pod := range eligiblePods {
		if _, found := pod.Labels[podDesignationLabel]; found {
			foundPodDesignated = true
			break
		}
	}
	if !foundPodDesignated && len(eligiblePods) > 0 {
		pod := eligiblePods[0]
		patch := client.MergeFrom(pod.DeepCopy())
		pod.Labels[podDesignationLabel] = "true"
		err = r.Patch(ctx, &pod, patch)
		if err != nil {
			return ctrl.Result{Requeue: true}, fmt.Errorf("Unable to patch pod with designation label: %s", err.Error())
		}
	}

	// pod designation label to selector to svc
	if _, found := svc.Spec.Selector[podDesignationLabel]; !found {
		patch := client.MergeFrom(svc.DeepCopy())
		svc.Spec.Selector[podDesignationLabel] = "true"
		err = r.Patch(ctx, svc, patch)
		if err != nil {
			return ctrl.Result{Requeue: true}, fmt.Errorf("Unable to patch svc with designation label: %s", err.Error())
		}
	}

	return ctrl.Result{}, nil
}

// updateStatus updates the status of the Core resource
func updateStatus(events []string, status *nodev1alpha1.NodeStatus, namespace string, name string, client client.Client, ctx context.Context) error {

	var err error

	logger := log.FromContext(ctx).WithValues("core", namespace+"/"+name)

	// get podNames for Status.Nodes
	status.Nodes, err = getPodNames(namespace, labelsForCore(name), client)
	if err != nil {
		logger.Error(err, "Failed to get pod names")
		return err
	}

	// Update status.Events
	status.Events = append(status.Events, events...)

	// if events is greater than 10, remove the oldest event
	if len(status.Events) > 10 {
		status.Events = status.Events[1:]
	}

	return nil
}

func createTopologyConfigMap(name string, namespace string, protocol string, network string, nodeSvc []string, addProducerNodes bool) (*corev1.ConfigMap, error) {

	topology := generateTopologyConfig(protocol, network, nodeSvc, addProducerNodes)

	// convert topology to json string
	topologyJSON, err := json.Marshal(topology)
	if err != nil {
		return nil, err
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-topology", name),
			Namespace: namespace,
		},
		Data: map[string]string{
			"topology.json": string(topologyJSON),
		},
	}, nil
}

// generateTopologyConfig generates the topology.json file for the cardano-node
func generateTopologyConfig(protocol string, network string, nodeSvc []string, addDefaultProducerNodes bool) Topology {
	topology := Topology{
		LocalRoots:         make([]TopologyRoot, 0),
		PublicRoots:        make([]TopologyRoot, 0),
		UseLedgerAfterSlot: 0,
	}

	// if the protocol is apexfusion, network is testnet
	// add addDefaultProducerNodes is true
	// the add the following addresses and their respective ports
	// address: relay-0.prime.testnet.apexfusion.org port: 31400
	// address: relay-1.prime.testnet.apexfusion.org port: 31400
	if addDefaultProducerNodes {
		if protocol == "apexfusion" && network == "testnet" {
			topology.PublicRoots = []TopologyRoot{
				{
					AccessPoints: []TopologyRootAccessPoint{
						{
							Address: "relay-0.prime.testnet.apexfusion.org",
							Port:    5521,
						},
						{
							Address: "relay-1.prime.testnet.apexfusion.org",
							Port:    5521,
						},
					},
					Advertise: true,
					Valency:   1,
				},
			}
		}
	}

	// default is no public roots
	if len(topology.PublicRoots) == 0 {
		topology.PublicRoots = []TopologyRoot{
			{
				AccessPoints: []TopologyRootAccessPoint{},
				Advertise:    false,
			},
		}
	}

	for i := 0; i < len(nodeSvc); i++ {
		topology.LocalRoots = append(topology.LocalRoots, TopologyRoot{
			AccessPoints: []TopologyRootAccessPoint{
				{
					Address: nodeSvc[i],
					Port:    defaultCardanoPort,
				},
			},
			Advertise: false,
			Valency:   1,
		})
	}

	return topology
}
