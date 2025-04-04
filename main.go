package main

import (
	"flag"
	"strings"
	"yyckv/kv"
	logger "yyckv/raft/log"
	"yyckv/raft/raftpb"
)

//TIP To run your code, right-click the code and select <b>Run</b>. Alternatively, click
// the <icon src="AllIcons.Actions.Execute"/> icon in the gutter and select the <b>Run</b> menu item from here.

func main() {
	cluster := flag.String("cluster", "http://127.0.0.1:9000", "comma separated cluster peers")
	id := flag.Int("id", 1, "node ID")
	kvport := flag.Int("port", 9121, "key-value server port")
	join := flag.Bool("join", false, "join an existing cluster")
	loggingLevel := flag.String("loggingLevel", "info", "log level")
	flag.Parse()

	logger.InitLoggerLevel(*loggingLevel)

	proposeC := make(chan string)
	defer close(proposeC)
	confChangeC := make(chan raftpb.ConfChange)
	defer close(confChangeC)

	var kvs *kv.Kvstore
	getSnapshot := func() ([]byte, error) { return kvs.GetSnapshot() }
	commitC, errorC, snapshotterReadyChannel := kv.NewRaftNode(
		*id,
		strings.Split(*cluster, ","),
		*join,
		getSnapshot,
		proposeC,
		confChangeC)
	// block until snapshotter (from bootstrap process) is ready
	snapshotter := <-snapshotterReadyChannel
	kvs = kv.NewKVStore(snapshotter, proposeC, commitC, errorC)

	serveHTTPKVAPI(kvs, *kvport, errorC)
}

//TIP See GoLand help at <a href="https://www.jetbrains.com/help/go/">jetbrains.com/help/go/</a>.
// Also, you can try interactive lessons for GoLand by selecting 'Help | Learn IDE Features' from the main menu.
