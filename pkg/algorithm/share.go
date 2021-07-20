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
	"sort"
	"math"

	"k8s.io/klog"

	"tkestack.io/gpu-admission/pkg/device"
)

type shareMode struct {
	node *device.NodeInfo
}

//NewShareMode returns a new shareMode struct.
//
//Evaluate() of shareMode returns one device with minimum available cores
//which fullfil the request.
//
//Share mode means multiple application may share one GPU device which uses
//GPU more efficiently.
func NewShareMode(n *device.NodeInfo) *shareMode {
	return &shareMode{n}
}

func (al *shareMode) Evaluate(cores uint, memory uint, estimatedTime uint) []*device.DeviceInfo {
	var (
		devs        []*device.DeviceInfo
		deviceCount = al.node.GetDeviceCount()
		tmpStore    = make([]*device.DeviceInfo, deviceCount)
		sorter      = shareModeSort(device.ByAllocatableCores, device.ByAllocatableMemory, device.ByID)
	)

	for i := 0; i < deviceCount; i++ {
		tmpStore[i] = al.node.GetDeviceMap()[i]
	}

	
	sorter.Sort(tmpStore)

	//此处实现TOPSIS算法
	var decisionMatrix [][]float64

	//构造决策矩阵
	for _, dev := range tmpStore {
		var nodeMatrix []float64
		nodeMatrix = append(nodeMatrix, float64(dev.AllocatableCores()))
		nodeMatrix = append(nodeMatrix, float64(dev.AllocatableMemory()))
		itime := int(estimatedTime) - int(dev.IsolatedTime())
		if itime < 0 {
			itime = 0
		}
		nodeMatrix = append(nodeMatrix, float64(itime))
		nodeMatrix = append(nodeMatrix, float64(dev.NumberofContainer()))
		decisionMatrix = append(decisionMatrix, nodeMatrix)
	}

	row := len(decisionMatrix)
	col := len(decisionMatrix[0])

	var tmp1 []float64

	for i := 0; i < col ;i++ {
		var sum float64
		for j :=0; j < row; j++ {
			sum = sum + decisionMatrix[j][i] * decisionMatrix[j][i]
		}
		tmp1 = append(tmp1, math.Sqrt(sum))
	}

	weight := []float64{0.3, 0.3, 0.2, 0.2}

	for i := 0; i < col; i++ {
		for j := 0; j < row; j++ {
			if tmp1[i] == 0 {
				decisionMatrix[j][i] = 0
			} else {
				decisionMatrix[j][i] = weight[i] * (decisionMatrix[j][i] / tmp1[i])
			}
		}
	}

	Amax := []float64{decisionMatrix[0][0], decisionMatrix[0][1], decisionMatrix[0][2], decisionMatrix[0][3]}
	Amin := []float64{decisionMatrix[0][0], decisionMatrix[0][1], decisionMatrix[0][2], decisionMatrix[0][3]}


	for i := 0; i < row; i++ {
		if Amax[0] < decisionMatrix[i][0] {
			Amax[0] = decisionMatrix[i][0]
		}
		if Amin[0] > decisionMatrix[i][0] {
			Amin[0] = decisionMatrix[i][0]
		}
 	}

	for i := 0; i < row; i++ {
		if Amax[1] < decisionMatrix[i][1] {
			Amax[1] = decisionMatrix[i][1]
		}
		if Amin[1] > decisionMatrix[i][1] {
			Amin[1] = decisionMatrix[i][1]
		}
 	}

	for i := 0; i < row; i++ {
		if Amax[2] < decisionMatrix[i][2] {
			Amax[2] = decisionMatrix[i][2]
		}
		if Amin[2] > decisionMatrix[i][2] {
			Amin[2] = decisionMatrix[i][2]
		}
 	}

	for i := 0; i < row; i++ {
		if Amax[3] > decisionMatrix[i][3] {
			Amax[3] = decisionMatrix[i][3]
		}
		if Amin[3] < decisionMatrix[i][3] {
			Amin[3] = decisionMatrix[i][3]
		}
 	}

	var SMmax, SMmin []float64
	for i := 0; i < row; i++ {
		var sum1, sum2 float64
		for j := 0; j < col; j++ {
			sum1 = sum1 + (decisionMatrix[i][j] - Amax[j]) * (decisionMatrix[i][j] - Amax[j])
			sum2 = sum2 + (decisionMatrix[i][j] - Amin[j]) * (decisionMatrix[i][j] - Amin[j])
		}
		SMmax = append(SMmax, math.Sqrt(sum1))
		SMmin = append(SMmin, math.Sqrt(sum2))
	}
	
	var RC []float64

	for i := 0; i < row; i++ {
		RC = append(RC, SMmin[i] / (SMmax[i] + SMmin[i]))
	}

	
	max := RC[0]
	var maxdev *device.DeviceInfo = tmpStore[0]
	for i, dev := range tmpStore {
		if RC[i] > max {
			max = RC[i]
			maxdev = dev
		}
		/*
		if dev.AllocatableCores() >= cores && dev.AllocatableMemory() >= memory {
			klog.V(4).Infof("Pick up %d , cores: %d, memory: %d",
				dev.GetID(), dev.AllocatableCores(), dev.AllocatableMemory())
			devs = append(devs, dev)
			br
		*/

	}
	devs = append(devs, maxdev)
	klog.V(4).Infof("Pick up %d , cores: %d, memory: %d",
				maxdev.GetID(), maxdev.AllocatableCores(), maxdev.AllocatableMemory())
	return devs
}

type shareModePriority struct {
	data []*device.DeviceInfo
	less []device.LessFunc
}

func shareModeSort(less ...device.LessFunc) *shareModePriority {
	return &shareModePriority{
		less: less,
	}
}

func (smp *shareModePriority) Sort(data []*device.DeviceInfo) {
	smp.data = data
	sort.Sort(smp)
}

func (smp *shareModePriority) Len() int {
	return len(smp.data)
}

func (smp *shareModePriority) Swap(i, j int) {
	smp.data[i], smp.data[j] = smp.data[j], smp.data[i]
}

func (smp *shareModePriority) Less(i, j int) bool {
	var k int

	for k = 0; k < len(smp.less)-1; k++ {
		less := smp.less[k]
		switch {
		case less(smp.data[i], smp.data[j]):
			return true
		case less(smp.data[j], smp.data[i]):
			return false
		}
	}

	return smp.less[k](smp.data[i], smp.data[j])
}
