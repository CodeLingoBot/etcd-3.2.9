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
	"math/rand"
	"sync"
	"time"

	"github.com/coreos/etcd/raft/raftpb"
)

// a network interface
type iface interface {
	send(m raftpb.Message)
	recv() chan raftpb.Message
	disconnect()
	connect()
}

// a network
type network interface {
	// drop message at given rate (1.0 drops all messages)
	drop(from, to uint64, rate float64)
	// delay message for (0, d] randomly at given rate (1.0 delay all messages)
	// do we need rate here?
	delay(from, to uint64, d time.Duration, rate float64)
	disconnect(id uint64)
	connect(id uint64)
	// heal heals the network
	heal()
}

type raftNetwork struct {
	mu           sync.Mutex                     // 操作 map 数据结构的时候需要加锁同步
	disconnected map[uint64]bool                // 类似于离线标志位
	dropmap      map[conn]float64               // 针对每条连接的 drop 配置
	delaymap     map[conn]delay                 // 针对每条连接的 delay 配置
	recvQueues   map[uint64]chan raftpb.Message // 每个 node 都有一条接收队列
}

// conn 表示一条链路
type conn struct {
	from, to uint64
}

// 该 delay 配置表示随机将 %rate 的消息 delay random(d) 微秒
type delay struct {
	d    time.Duration
	rate float64
}

func newRaftNetwork(nodes ...uint64) *raftNetwork {
	pn := &raftNetwork{
		recvQueues:   make(map[uint64]chan raftpb.Message),
		dropmap:      make(map[conn]float64),
		delaymap:     make(map[conn]delay),
		disconnected: make(map[uint64]bool),
	}

	// 为每一个节点都创建一条容量为 1024 的带缓冲的 channel 作为接收队列
	for _, n := range nodes {
		pn.recvQueues[n] = make(chan raftpb.Message, 1024)
	}
	return pn
}

func (rn *raftNetwork) nodeNetwork(id uint64) iface {
	return &nodeNetwork{id: id, raftNetwork: rn}
}

func (rn *raftNetwork) send(m raftpb.Message) {
	rn.mu.Lock()
	to := rn.recvQueues[m.To]  // 取出目的节点的接收队列
	if rn.disconnected[m.To] { // 如果目的节点已经不在线，则将目的节点设置为 nil
		to = nil
	}
	drop := rn.dropmap[conn{m.From, m.To}] // 取出链路的 drop 配置
	dl := rn.delaymap[conn{m.From, m.To}]  // 取出链路的 delay 配置
	rn.mu.Unlock()                         // 读操作结束，后续无 r/w 操作，顾可以解锁

	if to == nil { // 如果目标节点已经离线，无需发送，直接返回
		return
	}
	if drop != 0 && rand.Float64() < drop { // rand.Float64() 返回一个位于 [0.0,1.0) 的随机浮点数
		return
	}
	// TODO: shall we dl without blocking the send call?
	// delay 操作是会阻塞 send() 动作的，这是必要的吗 ？
	if dl.d != 0 && rand.Float64() < dl.rate {
		rd := rand.Int63n(int64(dl.d)) // rand.Int63n() 返回一个位于 [0,n) 非负随机整数
		time.Sleep(time.Duration(rd))
	}

	// use marshal/unmarshal to copy message to avoid data race.
	b, err := m.Marshal() // 下面的对消息的操作函数只是用 marshal() 进行解包，再用 unmarshal 进行组包并发送组包后的消息
	if err != nil {
		panic(err)
	}

	var cm raftpb.Message
	err = cm.Unmarshal(b)
	if err != nil {
		panic(err)
	}

	select {
	case to <- cm: // 投递消息到目的节点的接收队列里
	default: // 如果目的节点的接收队列已满，则直接丢弃这条消息
		// drop messages when the receiver queue is full.
	}
}

func (rn *raftNetwork) recvFrom(from uint64) chan raftpb.Message {
	rn.mu.Lock()
	fromc := rn.recvQueues[from]
	if rn.disconnected[from] {
		fromc = nil
	}
	rn.mu.Unlock()

	return fromc
}

func (rn *raftNetwork) drop(from, to uint64, rate float64) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.dropmap[conn{from, to}] = rate
}

func (rn *raftNetwork) delay(from, to uint64, d time.Duration, rate float64) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.delaymap[conn{from, to}] = delay{d, rate}
}

func (rn *raftNetwork) heal() {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.dropmap = make(map[conn]float64)
	rn.delaymap = make(map[conn]delay)
}

func (rn *raftNetwork) disconnect(id uint64) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.disconnected[id] = true
}

func (rn *raftNetwork) connect(id uint64) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.disconnected[id] = false
}

// nodeNetwork 实现了 iface 接口
type nodeNetwork struct {
	id uint64
	*raftNetwork
}

func (nt *nodeNetwork) connect() {
	nt.raftNetwork.connect(nt.id)
}

func (nt *nodeNetwork) disconnect() {
	nt.raftNetwork.disconnect(nt.id)
}

func (nt *nodeNetwork) send(m raftpb.Message) {
	nt.raftNetwork.send(m)
}

func (nt *nodeNetwork) recv() chan raftpb.Message {
	return nt.recvFrom(nt.id)
}
