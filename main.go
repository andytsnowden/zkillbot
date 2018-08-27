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

	// Cancel Context
	cContext, cSignal := context.WithCancel(bot.ctx)

	// Connect to Discord
	bot.connectDiscord()
	bot.connectzKillboardWS()
	bot.consumezKillboardWS(cContext)

	// Runner Threads
	go bot.eveIDLookupCmd(cContext)
	go bot.zKillboardReceive(cContext)
	go bot.zKillboardTrack(cContext)

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
