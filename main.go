package main

import (
	"os"
	"os/signal"
	"syscall"
)

func main() {

	// Init bot
	bot := NewZKillBot()

	// Connect to Discord
	bot.connectDiscord()
	go bot.eveIDLookupCmd()

	// Run forever unless we sig-exit
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly exit
	bot.discord.Close()
}
