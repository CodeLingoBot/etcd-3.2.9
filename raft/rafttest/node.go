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
	"log"
	"sync"
	"time"

	"github.com/coreos/etcd/raft"
	"github.com/coreos/etcd/raft/raftpb"
	"golang.org/x/net/context"
)

type node struct {
	raft.Node // Raft 集群的节点
	id        uint64
	iface     iface // network transport
	stopc     chan struct{}
	pausec    chan bool

	// stable
	storage *raft.MemoryStorage // 持久化存储

	mu    sync.Mutex // guards state
	state raftpb.HardState
}

func startNode(id uint64, peers []raft.Peer, iface iface) *node {
	st := raft.NewMemoryStorage()
	c := &raft.Config{
		ID:              id,
		ElectionTick:    10,
		HeartbeatTick:   1,
		Storage:         st,
		MaxSizePerMsg:   1024 * 1024,
		MaxInflightMsgs: 256,
	}
	rn := raft.StartNode(c, peers)
	n := &node{
		Node:    rn,
		id:      id,
		storage: st,
		iface:   iface,
		pausec:  make(chan bool),
	}
	n.start()
	return n
}

func (n *node) start() {
	n.stopc = make(chan struct{})
	ticker := time.Tick(5 * time.Millisecond) // 5ms 产生一次时钟信号

	go func() {
		for {
			select {
			case <-ticker:
				n.Tick()
			case rd := <-n.Ready():
				if !raft.IsEmptyHardState(rd.HardState) { // 如果此时节点的 HardState 不为空，更新 HardState
					n.mu.Lock()
					n.state = rd.HardState
					n.mu.Unlock()
					n.storage.SetHardState(n.state)
				}
				n.storage.Append(rd.Entries) // 持久化日志
				time.Sleep(time.Millisecond)
				// TODO: make send async, more like real world...
				// 发送环节应该异步化，这样才能更像真实的世界
				for _, m := range rd.Messages {
					n.iface.send(m)
				}
				n.Advance() // 进一步演化（advance）
			case m := <-n.iface.recv():
				go n.Step(context.TODO(), m) // 接收到新的消息
			case <-n.stopc:
				n.Stop()
				log.Printf("raft.%d: stop", n.id)
				n.Node = nil
				close(n.stopc)
				return
			case p := <-n.pausec:
				recvms := make([]raftpb.Message, 0)
				for p { // 如果收到 pause 信号（即暂停），节点会创建一个接收消息的 buffer，仍不停地接收消息，直至停止 pause 动作
					select {
					case m := <-n.iface.recv():
						recvms = append(recvms, m)
					case p = <-n.pausec:
					}
				}
				// step all pending messages
				for _, m := range recvms {
					n.Step(context.TODO(), m)
				}
			}
		}
	}()
}

// stop stops the node. stop a stopped node might panic.
// All in memory state of node is discarded.
// All stable MUST be unchanged.
func (n *node) stop() {
	n.iface.disconnect()
	n.stopc <- struct{}{}
	// wait for the shutdown
	<-n.stopc
}

// restart restarts the node. restart a started node
// blocks and might affect the future stop operation.
func (n *node) restart() {
	// wait for the shutdown
	<-n.stopc
	c := &raft.Config{
		ID:              n.id,
		ElectionTick:    10,
		HeartbeatTick:   1,
		Storage:         n.storage,
		MaxSizePerMsg:   1024 * 1024,
		MaxInflightMsgs: 256,
	}
	n.Node = raft.RestartNode(c)
	n.start()
	n.iface.connect()
}

// pause pauses the node.
// The paused node buffers the received messages and replies
// all of them when it resumes.
func (n *node) pause() {
	n.pausec <- true
}

// resume resumes the paused node.
func (n *node) resume() {
	n.pausec <- false
}
