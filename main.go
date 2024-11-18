package main

import (
	"flag"
)

//TIP To run your code, right-click the code and select <b>Run</b>. Alternatively, click
// the <icon src="AllIcons.Actions.Execute"/> icon in the gutter and select the <b>Run</b> menu item from here.

func main() {
	kvport := flag.Int("port", 9121, "key-value server port")
	flag.Parse()

	proposeC := make(chan string)
	defer close(proposeC)

	errorC := make(chan error)
	defer close(errorC)

	serveHTTPKVAPI(*kvport, errorC)
}

//TIP See GoLand help at <a href="https://www.jetbrains.com/help/go/">jetbrains.com/help/go/</a>.
// Also, you can try interactive lessons for GoLand by selecting 'Help | Learn IDE Features' from the main menu.
