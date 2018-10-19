package main

import (
    "testing"
)

func TestNewZKillBot(t *testing.T) {
    // Init the method
    bot := NewZKillBot()

    // Did viper find any keys
    // Default should always have several
    if len(bot.viperConfig.AllKeys()) == 0 {
        t.Logf("No configuration found, viper failed to set defaults or parse")
        t.Fail()
    }

}

func TestNewZKillBot_connectDiscord(t *testing.T) {
    // Init the method
    bot := NewZKillBot()

    // Setting the bot_token so discord can connect for the test
    //bot.viperConfig.Set("discord_bot_token", "NDgxNTY1MDU1Nzg4MjUzMjAy.DmRv5g.cvJK0mcPJKzKzJQAmo-weNChRmA")

    // Connect to discord
    bot.connectDiscord()

    if bot.discord.State.User.ID == "" {
        t.Logf("Discord failed to authenicate and start websocket connection")
        t.Fail()
    }

}