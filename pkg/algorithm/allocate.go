/*
 * Tencent is pleased to support the open source community by making TKEStack available.
 *
 * Copyright (C) 2012-2019 Tencent. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use
 * this file except in compliance with the License. You may obtain a copy of the
 * License at
 *
 * https://opensource.org/licenses/Apache-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
 * WARRANTIES OF ANY KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations under the License.
 */
package algorithm

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"k8s.io/api/core/v1"
	"k8s.io/klog"

	"tkestack.io/gpu-admission/pkg/device"
	"tkestack.io/gpu-admission/pkg/util"
)

type allocator struct {
	nodeInfo *device.NodeInfo
}

func NewAllocator(n *device.NodeInfo) *allocator {
	return &allocator{nodeInfo: n}
}

// IsAllocatable attempt to allocate containers which has GPU request of given pod
func (alloc *allocator) IsAllocatable(pod *v1.Pod) bool {
	allocatable := true
	for i, c := range pod.Spec.Containers {
		if !util.IsGPURequiredContainer(&c) {
			continue
		}
		_, err := alloc.AllocateOne(pod, i, &c)
		if err != nil {
			klog.Infof("failed to allocate for pod %s container %s", pod.UID, c.Name)
			allocatable = false
			break
		}
	}
	return allocatable
}

// Allocate tries to find a suitable GPU device for containers
// and records some data in pod's annotation
func (alloc *allocator) Allocate(pod *v1.Pod) (*v1.Pod, error) {
	newPod := pod.DeepCopy()
	if newPod.Annotations == nil {
		newPod.Annotations = make(map[string]string)
	}
	for i, c := range newPod.Spec.Containers {
		if !util.IsGPURequiredContainer(&c) {
			continue
		}
		devIDs := []string{}
		devs, err := alloc.AllocateOne(pod, i, &c)
		if err != nil {
			klog.Infof("failed to allocate for pod %s(%s)", newPod.Name, c.Name)
			return nil, err
		}
		for _, dev := range devs {
			devIDs = append(devIDs, strconv.Itoa(dev.GetID()))
		}
		newPod.Annotations[util.PredicateGPUIndexPrefix+strconv.Itoa(i)] = strings.Join(devIDs, ",")
	}
	newPod.Annotations[util.PredicateNode] = alloc.nodeInfo.GetName()
	newPod.Annotations[util.GPUAssigned] = "false"
	newPod.Annotations[util.PredicateTimeAnnotation] = fmt.Sprintf("%d", time.Now().UnixNano())

	return newPod, nil
}

// AllocateOne tries to allocate GPU devices for given container
func (alloc *allocator) AllocateOne(pod *v1.Pod, containerIndex int, container *v1.Container) ([]*device.DeviceInfo, error) {
	var (
		devs           []*device.DeviceInfo
		sharedMode     bool
		vcore, vmemory uint
	)
	node := alloc.nodeInfo.GetNode()
	//节点的总的显存快熟
	nodeTotalMemory := util.GetCapacityOfNode(node, util.VMemoryAnnotation)
	//GPU设备数量
	deviceCount := util.GetGPUDeviceCountOfNode(node)
	//每张卡的GPU显存数量
	deviceTotalMemory := uint(nodeTotalMemory / deviceCount)
	//容器请求的GPU份数
	needCores := util.GetGPUResourceOfContainer(container, util.VCoreAnnotation)
	//容器所需的显存块数
	needMemory := util.GetGPUResourceOfContainer(container, util.VMemoryAnnotation)
	//容器的预测执行时间
	estimatedTime, err := util.GetEstimatedTimeOfContainer(pod, containerIndex)
	if err != nil {
		return devs, err
	}

	switch {
	case needCores < util.HundredCore:
		devs = NewShareMode(alloc.nodeInfo).Evaluate(needCores, needMemory, estimatedTime)
		sharedMode = true
	default:
		devs = NewExclusiveMode(alloc.nodeInfo).Evaluate(needCores, needMemory)
	}

	if len(devs) == 0 {
		return nil, fmt.Errorf("failed to allocate for container %s", container.Name)
	}

	if sharedMode {
		vcore = needCores
		vmemory = needMemory
	} else {
		vcore = util.HundredCore
		vmemory = deviceTotalMemory
	}

	// record this container GPU request, we don't rollback data if an error happened,
	// because any container failed to be allocated will cause the predication failed
	for _, dev := range devs {
		//新加入的container，已执行时间为 0
		err := alloc.nodeInfo.AddUsedResources(dev.GetID(), vcore, vmemory, int(estimatedTime))
		if err != nil {
			klog.Infof("failed to update used resource for node %s dev %d due to %v",
				node.Name, dev.GetID(), err)
			return nil, err
		}
	}
	return devs, nil
}
