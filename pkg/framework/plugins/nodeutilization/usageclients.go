/*
Copyright 2024 The Kubernetes Authors.

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

package nodeutilization

import (
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
	utilptr "k8s.io/utils/ptr"
	nodeutil "sigs.k8s.io/descheduler/pkg/descheduler/node"
	podutil "sigs.k8s.io/descheduler/pkg/descheduler/pod"
	"sigs.k8s.io/descheduler/pkg/utils"
)

type usageClient interface {
	// Both low/high node utilization plugins are expected to invoke sync right
	// after Balance method is invoked. There's no cache invalidation so each
	// Balance is expected to get the latest data by invoking sync.
	sync(nodes []*v1.Node) error
	nodeUtilization(node string) map[v1.ResourceName]*resource.Quantity
	pods(node string) []*v1.Pod
	podUsage(pod *v1.Pod) (map[v1.ResourceName]*resource.Quantity, error)
}

type requestedUsageClient struct {
	resourceNames         []v1.ResourceName
	getPodsAssignedToNode podutil.GetPodsAssignedToNodeFunc

	_pods            map[string][]*v1.Pod
	_nodeUtilization map[string]map[v1.ResourceName]*resource.Quantity
}

var _ usageClient = &requestedUsageClient{}

func newRequestedUsageClient(
	resourceNames []v1.ResourceName,
	getPodsAssignedToNode podutil.GetPodsAssignedToNodeFunc,
) *requestedUsageClient {
	return &requestedUsageClient{
		resourceNames:         resourceNames,
		getPodsAssignedToNode: getPodsAssignedToNode,
	}
}

func (s *requestedUsageClient) nodeUtilization(node string) map[v1.ResourceName]*resource.Quantity {
	return s._nodeUtilization[node]
}

func (s *requestedUsageClient) pods(node string) []*v1.Pod {
	return s._pods[node]
}

func (s *requestedUsageClient) podUsage(pod *v1.Pod) (map[v1.ResourceName]*resource.Quantity, error) {
	usage := make(map[v1.ResourceName]*resource.Quantity)
	for _, resourceName := range s.resourceNames {
		usage[resourceName] = utilptr.To[resource.Quantity](utils.GetResourceRequestQuantity(pod, resourceName).DeepCopy())
	}
	return usage, nil
}

func (s *requestedUsageClient) sync(nodes []*v1.Node) error {
	s._nodeUtilization = make(map[string]map[v1.ResourceName]*resource.Quantity)
	s._pods = make(map[string][]*v1.Pod)

	for _, node := range nodes {
		pods, err := podutil.ListPodsOnANode(node.Name, s.getPodsAssignedToNode, nil)
		if err != nil {
			klog.V(2).InfoS("Node will not be processed, error accessing its pods", "node", klog.KObj(node), "err", err)
			return fmt.Errorf("error accessing %q node's pods: %v", node.Name, err)
		}

		nodeUsage, err := nodeutil.NodeUtilization(pods, s.resourceNames, func(pod *v1.Pod) (v1.ResourceList, error) {
			req, _ := utils.PodRequestsAndLimits(pod)
			return req, nil
		})
		if err != nil {
			return err
		}

		// store the snapshot of pods from the same (or the closest) node utilization computation
		s._pods[node.Name] = pods
		s._nodeUtilization[node.Name] = nodeUsage
	}

	return nil
}