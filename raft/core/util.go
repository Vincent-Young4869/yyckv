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
