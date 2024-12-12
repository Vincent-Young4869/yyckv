package main

import (
	"flag"
	"strings"
	"yyckv/kv"
	"yyckv/raft/raftpb"
)

//TIP To run your code, right-click the code and select <b>Run</b>. Alternatively, click
// the <icon src="AllIcons.Actions.Execute"/> icon in the gutter and select the <b>Run</b> menu item from here.

func main() {
	cluster := flag.String("cluster", "http://127.0.0.1:9000", "comma separated cluster peers")
	id := flag.Int("id", 1, "node ID")
	kvport := flag.Int("port", 9000, "key-value server port")
	join := flag.Bool("join", false, "join an existing cluster")
	flag.Parse()

	confChangeC := make(chan raftpb.ConfChange)
	defer close(confChangeC)

	errorC := kv.NewRaftNode(*id, strings.Split(*cluster, ","), *join, confChangeC)
	kvStore := kv.NewKVStore(errorC)

	serveHTTPKVAPI(kvStore, *kvport, errorC)
}

//TIP See GoLand help at <a href="https://www.jetbrains.com/help/go/">jetbrains.com/help/go/</a>.
// Also, you can try interactive lessons for GoLand by selecting 'Help | Learn IDE Features' from the main menu.
