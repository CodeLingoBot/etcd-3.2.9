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

package raft

import pb "github.com/coreos/etcd/raft/raftpb"

// unstable.entries[i] has raft log position i+unstable.offset.
// Note that unstable.offset may be less than the highest log
// position in storage; this means that the next write to storage
// might need to truncate the log before persisting unstable.entries.
type unstable struct {
	// the incoming unstable snapshot, if any.
	snapshot *pb.Snapshot
	// all entries that have not yet been written to storage.
	entries []pb.Entry
	offset  uint64 // offset 指的是第一个 log entry 的 index，可以不为 0，于 slice 的第 1 项（索引为 0）要区分一下

	logger Logger
}

// maybeFirstIndex returns the index of the first possible entry in entries
// if it has a snapshot.

// 如果 snapshot 不为空，则 firstIndex = snapshot.Metadata.Index + 1
// 否则为 0
// 逻辑上可以这么认为：| snapshot | [ offset, offset+1, offset+2, ..., offset+len(entries)-1 ]
func (u *unstable) maybeFirstIndex() (uint64, bool) {
	if u.snapshot != nil {
		return u.snapshot.Metadata.Index + 1, true
	}
	return 0, false
}

// maybeLastIndex returns the last index if it has at least one
// unstable entry or snapshot.

// 如果日志不为空，返回最后一个表项的 index
// 如果没有日志且有快照，只要返回快照中的 index 即可
func (u *unstable) maybeLastIndex() (uint64, bool) {
	if l := len(u.entries); l != 0 {
		return u.offset + uint64(l) - 1, true
	}
	if u.snapshot != nil {
		return u.snapshot.Metadata.Index, true
	}
	return 0, false
}

// maybeTerm returns the term of the entry at index i, if there
// is any.

// 返回指定 index 的 term
func (u *unstable) maybeTerm(i uint64) (uint64, bool) {
	if i < u.offset {
		if u.snapshot == nil {
			return 0, false
		}
		if u.snapshot.Metadata.Index == i {
			return u.snapshot.Metadata.Term, true
		}
		return 0, false
	}

	last, ok := u.maybeLastIndex()
	if !ok {
		return 0, false
	}
	if i > last {
		return 0, false
	}
	return u.entries[i-u.offset].Term, true
}

// 给定 index 和 term 的日志进行持久化
// 其实这是一个删除操作，如果通过约束检查（即 index 和 term 可以在 unstable entry 中匹配得上且 index 位于范围内）
// 删除[offset, index] 的日志，顺便做一下 shrink 动作
func (u *unstable) stableTo(i, t uint64) {
	gt, ok := u.maybeTerm(i)
	if !ok {
		return
	}
	// if i < offset, term is matched with the snapshot
	// only update the unstable entries if term is matched with
	// an unstable entry.

	if gt == t && i >= u.offset {
		u.entries = u.entries[i+1-u.offset:]
		u.offset = i + 1
		u.shrinkEntriesArray()
	}
}

// shrinkEntriesArray discards the underlying array used by the entries slice
// if most of it isn't being used. This avoids holding references to a bunch of
// potentially large entries that aren't needed anymore. Simply clearing the
// entries wouldn't be safe because clients might still be using them.

// 缩小底层 slice 的大小，避免太多无用的 slice 空间
func (u *unstable) shrinkEntriesArray() {
	// We replace the array if we're using less than half of the space in
	// it. This number is fairly arbitrary, chosen as an attempt to balance
	// memory usage vs number of allocations. It could probably be improved
	// with some focused tuning.
	const lenMultiple = 2
	if len(u.entries) == 0 {
		u.entries = nil
	} else if len(u.entries)*lenMultiple < cap(u.entries) {
		newEntries := make([]pb.Entry, len(u.entries))
		copy(newEntries, u.entries)
		u.entries = newEntries
	}
}

// 持久化 snapshot，如果是当前的 snapshot，就将 unstable 的 snapshot 删掉
// 留意：log_unstable.go 里面的 stablexxx 其实都是个删除动作，从缓存中删除
func (u *unstable) stableSnapTo(i uint64) {
	if u.snapshot != nil && u.snapshot.Metadata.Index == i {
		u.snapshot = nil
	}
}

// 从一个 snapshot 去重置 unstable 日志
// 我认为这是一个 reset 语义
func (u *unstable) restore(s pb.Snapshot) {
	u.offset = s.Metadata.Index + 1
	u.entries = nil
	u.snapshot = &s
}

// 这是一个拼接动作，留心了 ！
func (u *unstable) truncateAndAppend(ents []pb.Entry) {
	after := ents[0].Index // 待添加的 log entry 的第一个 index
	switch {
	// 如果待添加的日志刚好能衔接已有的日志，直接添加
	case after == u.offset+uint64(len(u.entries)): // u.offset+uint64(len(u.entries)) 是日志中下一个位置的 index
		// after is the next index in the u.entries
		// directly append
		u.entries = append(u.entries, ents...)
	// 如果待添加的日志从逻辑上要先于已有的日志，舍弃旧的日志，直接用新的日志
	case after <= u.offset:
		u.logger.Infof("replace the unstable entries from index %d", after)
		// The log is being truncated to before our current offset
		// portion, so set the offset and replace the entries
		u.offset = after
		u.entries = ents
	default:
		// truncate to after and copy to u.entries
		// then append
		// 如果待添加的日志是位于已有日志的中间，则保留已有日志前面的部分，后面部分直接使用新日志
		u.logger.Infof("truncate the unstable entries before index %d", after)
		u.entries = append([]pb.Entry{}, u.slice(u.offset, after)...)
		u.entries = append(u.entries, ents...)
	}
}

// unstable log 的切片操作，语义与 go 的切片操作一致
func (u *unstable) slice(lo uint64, hi uint64) []pb.Entry {
	u.mustCheckOutOfBounds(lo, hi)
	return u.entries[lo-u.offset : hi-u.offset]
}

// u.offset <= lo <= hi <= u.offset+len(u.entries)

// 这是一个约束性检查，对 slice(lo,hi) 的调用必须通过这个约束性检查
func (u *unstable) mustCheckOutOfBounds(lo, hi uint64) {
	if lo > hi {
		u.logger.Panicf("invalid unstable.slice %d > %d", lo, hi)
	}
	upper := u.offset + uint64(len(u.entries))
	if lo < u.offset || hi > upper {
		u.logger.Panicf("unstable.slice[%d,%d) out of bound [%d,%d]", lo, hi, u.offset, upper)
	}
}
