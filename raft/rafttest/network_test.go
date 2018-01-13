// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rafttest

import (
	"testing"
	"time"

	"github.com/coreos/etcd/raft/raftpb"
)

/*
	创建一个节点 1 到 2 的链路
	然后发送 1000 条消息，并随机丢弃其中 10%
	最后看收到的消息数量是否真的丢了 10%
*/
func TestNetworkDrop(t *testing.T) {
	// drop around 10% messages
	sent := 1000
	droprate := 0.1
	nt := newRaftNetwork(1, 2)
	nt.drop(1, 2, droprate)
	for i := 0; i < sent; i++ {
		nt.send(raftpb.Message{From: 1, To: 2})
	}

	c := nt.recvFrom(2) // 返回接收者的消息接收队列

	received := 0
	done := false
	for !done {
		select {
		case <-c:
			received++
		default:
			done = true
		}
	}

	drop := sent - received // received 表示已经收到的消息数量，drop 即是丢弃的消息数量
	// 检查 drop 是否在误差范围 10% 内
	if drop > int((droprate+0.1)*float64(sent)) || drop < int((droprate-0.1)*float64(sent)) {
		t.Errorf("drop = %d, want around %.2f", drop, droprate*float64(sent))
	}
}

/*
	创建一个节点 1 到 2 的链路
	然后发送 1000 条消息，并随机 delay 其中的 10%，每条 delay 1ms
	最后看总的发送时间是否和预估的 delay 时间一致
*/
func TestNetworkDelay(t *testing.T) {
	sent := 1000
	delay := time.Millisecond
	delayrate := 0.1
	nt := newRaftNetwork(1, 2)

	nt.delay(1, 2, delay, delayrate)
	var total time.Duration
	for i := 0; i < sent; i++ {
		s := time.Now()
		nt.send(raftpb.Message{From: 1, To: 2}) // 如果该消息被 delay，则在发送此条消息的时候将会被同步 delay 1ms
		total += time.Since(s)
	}

	// w 就是一个预估值，即假设 delay 数量一般的消息都 delay 了 'delay' 微妙
	w := time.Duration(float64(sent)*delayrate/2) * delay
	// there is some overhead in the send call since it generates random numbers.
	if total < w {
		t.Errorf("total = %v, want > %v", total, w)
	}
}
