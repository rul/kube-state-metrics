/*
Copyright 2016 The Kubernetes Authors All rights reserved.

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

package main

import (
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/1.4/pkg/api/v1"
)

var (
	descNodeInfo = prometheus.NewDesc(
		"kube_node_info",
		"Information about a cluster node.",
		[]string{
			"node",
			"kernel_version",
			"os_image",
			"container_runtime_version",
			"kubelet_version",
			"kubeproxy_version",
		}, nil,
	)

	descNodeStatusReady = prometheus.NewDesc(
		"kube_node_status_ready",
		"The ready status of a cluster node.",
		[]string{"node", "condition"}, nil,
	)
	descNodeStatusOutOfDisk = prometheus.NewDesc(
		"kube_node_status_out_of_disk",
		"Whether the node is out of disk space",
		[]string{"node", "condition"}, nil,
	)
	descNodeStatusPhase = prometheus.NewDesc(
		"kube_node_status_phase",
		"The phase the node is currently in.",
		[]string{"node", "phase"}, nil,
	)

	descNodeStatusCapacityPods = prometheus.NewDesc(
		"kube_node_status_capacity_pods",
		"The total pod resources of the node.",
		[]string{"node"}, nil,
	)
	descNodeStatusCapacityCPU = prometheus.NewDesc(
		"kube_node_status_capacity_cpu_cores",
		"The total CPU resources of the node.",
		[]string{"node"}, nil,
	)
	descNodeStatusCapacityMemory = prometheus.NewDesc(
		"kube_node_status_capacity_memory_bytes",
		"The total memory resources of the node.",
		[]string{"node"}, nil,
	)

	descNodeStatusAllocateablePods = prometheus.NewDesc(
		"kube_node_status_allocateable_pods",
		"The pod resources of a node that are available for scheduling.",
		[]string{"node"}, nil,
	)
	descNodeStatusAllocateableCPU = prometheus.NewDesc(
		"kube_node_status_allocateable_cpu_cores",
		"The CPU resources of a node that are available for scheduling.",
		[]string{"node"}, nil,
	)
	descNodeStatusAllocateableMemory = prometheus.NewDesc(
		"kube_node_status_allocateable_memory_bytes",
		"The memory resources of a node that are available for scheduling.",
		[]string{"node"}, nil,
	)
	descNodeSpecUnschedulable = prometheus.NewDesc(
		"kube_node_spec_unschedulable",
		"Whether a node can schedule new pods.",
		[]string{"node"}, nil,
	)
)

type nodeStore interface {
	List() (v1.NodeList, error)
}

// nodeCollector collects metrics about all nodes in the cluster.
type nodeCollector struct {
	store nodeStore
}

// Describe implements the prometheus.Collector interface.
func (nc *nodeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descNodeInfo
	ch <- descNodeStatusReady
	ch <- descNodeStatusOutOfDisk
	ch <- descNodeStatusPhase
	ch <- descNodeStatusCapacityCPU
	ch <- descNodeStatusCapacityMemory
	ch <- descNodeStatusCapacityPods
	ch <- descNodeStatusAllocateableCPU
	ch <- descNodeStatusAllocateableMemory
	ch <- descNodeStatusAllocateablePods
	ch <- descNodeSpecUnschedulable
}

// Collect implements the prometheus.Collector interface.
func (nc *nodeCollector) Collect(ch chan<- prometheus.Metric) {
	nodes, err := nc.store.List()
	if err != nil {
		glog.Errorf("listing nodes failed: %s", err)
		return
	}
	for _, n := range nodes.Items {
		nc.collectNode(ch, n)
	}
}

func (nc *nodeCollector) collectNode(ch chan<- prometheus.Metric, n v1.Node) {
	addGauge := func(desc *prometheus.Desc, v float64, lv ...string) {
		lv = append([]string{n.Name}, lv...)
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v, lv...)
	}
	// NOTE: the instrumentation API requires providing label values in order of declaration
	// in the metric descriptor. Be careful when making modifications.
	addGauge(descNodeInfo, 1,
		n.Status.NodeInfo.KernelVersion,
		n.Status.NodeInfo.OSImage,
		n.Status.NodeInfo.ContainerRuntimeVersion,
		n.Status.NodeInfo.KubeletVersion,
		n.Status.NodeInfo.KubeProxyVersion,
	)

	addGauge(descNodeSpecUnschedulable, boolFloat64(n.Spec.Unschedulable))

	// Collect node conditions and while default to false.
	// TODO(fabxc): add remaining conditions: NodeMemoryPressure,  NodeDiskPressure, NodeNetworkUnavailable
	for _, c := range n.Status.Conditions {
		switch c.Type {
		case v1.NodeReady:
			addConditionMetrics(ch, descNodeStatusReady, c.Status, n.Name)
		case v1.NodeOutOfDisk:
			addConditionMetrics(ch, descNodeStatusOutOfDisk, c.Status, n.Name)
		}
	}

	// Set current phase to 1, others to 0 if it is set.
	if p := n.Status.Phase; p != "" {
		addGauge(descNodeStatusPhase, boolFloat64(p == v1.NodePending), string(v1.NodePending))
		addGauge(descNodeStatusPhase, boolFloat64(p == v1.NodeRunning), string(v1.NodeRunning))
		addGauge(descNodeStatusPhase, boolFloat64(p == v1.NodeTerminated), string(v1.NodeTerminated))
	}

	// Add capacity and allocateable resources if they are set.
	addResource := func(d *prometheus.Desc, res v1.ResourceList, n v1.ResourceName) {
		if v, ok := res[n]; ok {
			addGauge(d, float64(v.Value()))
		}
	}
	addResource(descNodeStatusCapacityCPU, n.Status.Capacity, v1.ResourceCPU)
	addResource(descNodeStatusCapacityMemory, n.Status.Capacity, v1.ResourceMemory)
	addResource(descNodeStatusCapacityPods, n.Status.Capacity, v1.ResourcePods)

	addResource(descNodeStatusAllocateableCPU, n.Status.Allocatable, v1.ResourceCPU)
	addResource(descNodeStatusAllocateableMemory, n.Status.Allocatable, v1.ResourceMemory)
	addResource(descNodeStatusAllocateablePods, n.Status.Allocatable, v1.ResourcePods)
}

// addConditionMetrics generates one metric for each possible node condition
// status. For this function to work properly, the last label in the metric
// description must be the condition.
func addConditionMetrics(ch chan<- prometheus.Metric, desc *prometheus.Desc, cs v1.ConditionStatus, lv ...string) {
	ch <- prometheus.MustNewConstMetric(
		desc, prometheus.GaugeValue, boolFloat64(cs == v1.ConditionTrue),
		append(lv, "true")...,
	)
	ch <- prometheus.MustNewConstMetric(
		desc, prometheus.GaugeValue, boolFloat64(cs == v1.ConditionFalse),
		append(lv, "false")...,
	)
	ch <- prometheus.MustNewConstMetric(
		desc, prometheus.GaugeValue, boolFloat64(cs == v1.ConditionUnknown),
		append(lv, "unknown")...,
	)
}

func boolFloat64(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
