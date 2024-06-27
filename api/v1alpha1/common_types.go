package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
)

// NodeSpec ...
type NodeSpec struct {
	Replicas            int32                        `json:"replicas"`
	ImagePullSecrets    []v1.LocalObjectReference    `json:"imagePullSecrets,omitempty"`
	Image               string                       `json:"image,omitempty"`
	Storage             v1.PersistentVolumeClaimSpec `json:"storage"`
	Service             NodeServiceSpec              `json:"service,omitempty"`
	Resources           v1.ResourceRequirements      `json:"resources,omitempty"`
	ConfigurationConfig v1.LocalObjectReference      `json:"configuration,omitempty"`
	GenesisConfig       v1.LocalObjectReference      `json:"genesis,omitempty"`
	TopologyConfig      v1.LocalObjectReference      `json:"topology,omitempty"`
}

// NodeServiceSpec ...
type NodeServiceSpec struct {
	Annotations map[string]string `json:"annotations,omitempty"`
	Type        v1.ServiceType    `json:"type,omitempty"`
	Port        int32             `json:"port,omitempty"`
}

// NodeStatus defines the observed state of Core
type NodeStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Nodes  []string `json:"nodes"`
	Events []string `json:"events"`
}
