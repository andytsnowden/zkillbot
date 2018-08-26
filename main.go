package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Init bot
	bot := NewZKillBot()

	// Connect to Discord
	bot.connectDiscord()
	bot.connectzKillboardWS()

	// Cancel Context
	cContext, cSignal := context.WithCancel(bot.ctx)

	// Runner Threads
	go bot.eveIDLookupCmd(cContext)
	go bot.zKillboardRecieve(cContext)

	// Run forever unless we sig-exit
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Trigger thread cancels
	cSignal()

	// Cleanly exit
	bot.discord.Close()
	bot.zKillboard.Close()
}
