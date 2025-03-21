package core

import (
	"fmt"
	pb "yyckv/raft/raftpb"
)

var isLocalMsg = [...]bool{
	pb.MsgHup:               true,
	pb.MsgBeat:              true,
	pb.MsgUnreachable:       true,
	pb.MsgSnapStatus:        true,
	pb.MsgCheckQuorum:       true,
	pb.MsgStorageAppend:     true,
	pb.MsgStorageAppendResp: true,
	pb.MsgStorageApply:      true,
	pb.MsgStorageApplyResp:  true,
}

var isResponseMsg = [...]bool{
	pb.MsgAppResp:           true,
	pb.MsgVoteResp:          true,
	pb.MsgHeartbeatResp:     true,
	pb.MsgUnreachable:       true,
	pb.MsgReadIndexResp:     true,
	pb.MsgPreVoteResp:       true,
	pb.MsgStorageAppendResp: true,
	pb.MsgStorageApplyResp:  true,
}

func IsLocalMsg(msgt pb.MessageType) bool {
	return isMsgInArray(msgt, isLocalMsg[:])
}

func isMsgInArray(msgt pb.MessageType, arr []bool) bool {
	i := int(msgt)
	return i < len(arr) && arr[i]
}

func IsLocalMsgTarget(id uint64) bool {
	return id == LocalAppendThread || id == LocalApplyThread
}

func voteRespMsgType(msgt pb.MessageType) pb.MessageType {
	switch msgt {
	case pb.MsgVote:
		return pb.MsgVoteResp
	case pb.MsgPreVote:
		return pb.MsgPreVoteResp
	default:
		panic(fmt.Sprintf("not a vote message: %s", msgt))
	}
}

func IsResponseMsg(msgt pb.MessageType) bool {
	return isMsgInArray(msgt, isResponseMsg[:])
}

// entryEncodingSize represents the protocol buffer encoding size of one or more
// entries.
type entryEncodingSize uint64

func entsSize(ents []pb.Entry) entryEncodingSize {
	var size entryEncodingSize
	for _, ent := range ents {
		size += entryEncodingSize(ent.Size())
	}
	return size
}

// limitSize returns the longest prefix of the given entries slice, such that
// its total byte size does not exceed maxSize. Always returns a non-empty slice
// if the input is non-empty, so, as an exception, if the size of the first
// entry exceeds maxSize, a non-empty slice with just this entry is returned.
func limitSize(ents []pb.Entry, maxSize entryEncodingSize) []pb.Entry {
	if len(ents) == 0 {
		return ents
	}
	size := ents[0].Size()
	for limit := 1; limit < len(ents); limit++ {
		size += ents[limit].Size()
		if entryEncodingSize(size) > maxSize {
			return ents[:limit]
		}
	}
	return ents
}

// extend appends vals to the given dst slice. It differs from the standard
// slice append only in the way it allocates memory. If cap(dst) is not enough
// for appending the values, precisely size len(dst)+len(vals) is allocated.
//
// Use this instead of standard append in situations when this is the last
// append to dst, so there is no sense in allocating more than needed.
func extend(dst, vals []pb.Entry) []pb.Entry {
	need := len(dst) + len(vals)
	if need <= cap(dst) {
		return append(dst, vals...) // does not allocate
	}
	buf := make([]pb.Entry, need, need) // allocates precisely what's needed
	copy(buf, dst)
	copy(buf[len(dst):], vals)
	return buf
}
