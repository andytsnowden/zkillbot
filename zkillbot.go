package main

import (
	"fmt"
	"os"

	"regexp"
	"time"

	"encoding/json"
	"github.com/bwmarrin/discordgo"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

/*
   Init function for setting up basic config/logging and method struct
*/
func NewZKillBot() ZKillBot {
	// Command line flags
	pflag.Bool("verbose", false, "Print logs to command line")
	pflag.Parse()

	// Config file and locations
	viper.SetConfigName("zkillbot")
	viper.SetConfigType("json")
	viper.AddConfigPath(".")
	viper.AddConfigPath("$HOME/.zkillbot")

	// Set Defaults
	viper.SetDefault("discord_bot_token", "")
	viper.SetDefault("log_to_file", false)
	viper.SetDefault("log_level", "INFO")
	viper.SetDefault("log_file_path", "zkillbot.log")

	// Read in or create then read config
	err := viper.ReadInConfig()
	if err != nil {
		// attempt to write base config file
		file, _ := os.OpenFile("zkillbot.json", os.O_RDONLY|os.O_CREATE, 0666)
		defer file.Close()
		err := viper.WriteConfig()
		if err != nil {
			fmt.Printf("Fatal error config file: %s \n", err)
			os.Exit(1)
		}
	}

	// Init logging
	log := ConfigureLogging(viper.GetViper())
	log.Debug("Logging initialized")

	// Init channels
	zkillGroupLookup := make(chan discordCommand, 5)

	return ZKillBot{
		viperConfig: viper.GetViper(),
		log:         log,
		eveIDLookup: zkillGroupLookup,
	}
}

/*
	Connect to discord and set object
*/
func (bot *ZKillBot) connectDiscord() {
	log := bot.log
	discordToken := bot.viperConfig.GetString("discord_bot_token")

	// bail on missing token
	if len(discordToken) == 0 {
		log.Fatal("discord_bot_token is missing or invalid in configuration file")
	}

	// Start session
	discord, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		log.Fatalf("Failed to start discord session: %v", err)
	}

	// Pass session into method struct
	bot.discord = discord

	// Register callback for messages
	discord.AddHandler(bot.discordReceive)

	// Open websocket connection and start listening for messages
	err = discord.Open()
	if err != nil {
		log.Fatalf("Failed to start discord websocket session: %v", err)
	}
}

/*
	Call back for messages from any discord channel/server
	After classification we send commands down specific channels as to not block discord
*/
func (bot ZKillBot) discordReceive(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore my own messages
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Handle ID Lookup
	lookUpRegexp := regexp.MustCompile(`\!lookup.*`)
	if lookUpRegexp.MatchString(m.Content) {
		// throw into command chan
		bot.eveIDLookup <- discordCommand{
			ChannelID: m.ChannelID,
			Message:   m.Content,
		}
		return
	}

	bot.discord.ChannelMessageSend(m.ChannelID, "pong")
}

/*
	For a string look up the eve ID and type
	The ID will be needed when creating the websocket connection to zkill
*/
func (bot ZKillBot) eveIDLookupCmd() {
	log := bot.log
	//discord := bot.discord

	log.Debug("Starting Eve-ID lookup thread")
	for {
		select {
		// on message do work
		case message := <-bot.eveIDLookup:
			log.Debugf("Lookup command received: %v, %v", message.Message, message.ChannelID)

			// Remove command prefix
			msg := regexp.MustCompile(`!lookup\s`).ReplaceAllString(message.Message, "")

			// Create json for query
			apiRequestJSON, err := json.Marshal(eveUniverseIDLookup{msg})
			if err != nil {
				log.Errorf("failed to marshal json request: %v")
				break
			}

			// make do stuff

		default:
			// don't murder the cpu
			time.Sleep(500 * time.Nanosecond)
		}
	}
	log.Debug("Exited Eve-ID lookup thread")
}
