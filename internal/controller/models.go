package controller

type Topology struct {
	LocalRoots         []TopologyRoot `json:"localRoots"`
	PublicRoots        []TopologyRoot `json:"publicRoots"`
	UseLedgerAfterSlot int            `json:"useLedgerAfterSlot"`
}

type TopologyRoot struct {
	AccessPoints []TopologyRootAccessPoint `json:"accessPoints"`
	Advertise    bool                      `json:"advertise"`
	Valency      int                       `json:"valency,omitempty"`
}

type TopologyRootAccessPoint struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
}
