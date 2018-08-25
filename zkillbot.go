package main

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"encoding/json"
	"github.com/bwmarrin/discordgo"
	"github.com/parnurzeal/gorequest"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"net/http"
)

const ESIURL = "https://esi.evetech.net/latest/"

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

	// Init gorequest
	request := gorequest.New()

	return ZKillBot{
		viperConfig: viper.GetViper(),
		log:         log,
		eveIDLookup: zkillGroupLookup,
		request:     request,
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
	request := bot.request
	discord := bot.discord

	log.Debug("Starting Eve-ID lookup thread")
	for {
		select {
		// on message do work
		case message := <-bot.eveIDLookup:
			log.Debugf("Lookup command received: %v, %v", message.Message, message.ChannelID)

			// Remove command prefix and format request
			msg := regexp.MustCompile(`!lookup\s`).ReplaceAllString(message.Message, "")
			searchString := fmt.Sprintf(`["%v"]`, msg)

			// Send request
			resp, body, errs := request.Post(ESIURL + "universe/ids/").Send(searchString).EndBytes()
			if errs != nil {
				log.Errorf("EVE ESI request failed: %v", errs)
				discord.ChannelMessageSend(message.ChannelID, "EVE ESI error, unable to perform lookup at this time.")
				break
			}

			if resp.StatusCode != http.StatusOK {
				log.Errorf("EVE ESI request failed code: %v, err: %v", resp.StatusCode, resp.Body)
				discord.ChannelMessageSend(message.ChannelID, "EVE ESI error, unable to perform lookup at this time.")
				break
			}

			// Unmarshal response body
			esiResp := eveUniverseIDResponse{}
			err := json.Unmarshal(body, &esiResp)
			if err != nil {
				log.Errorf("EVE ESI request failed: %v", errs)
				discord.ChannelMessageSend(message.ChannelID, "EVE ESI error, unable to perform lookup at this time.")
				break
			}

			// Handle response json
			// We check if there is exactly 1 match for each sub-type, if so we return that ID
			if len(esiResp.Alliances) == 1 {
				log.Infof("Alliance ID found for: %v, ID: %v", esiResp.Alliances[0].Name, esiResp.Alliances[0].ID)
				discord.ChannelMessageSend(message.ChannelID, fmt.Sprintf(`Alliance ID: %v, found for name: %v`, esiResp.Alliances[0].ID, esiResp.Alliances[0].Name))
				break
			}

			if len(esiResp.Corporations) == 1 {
				log.Infof("Corporation ID found for: %v, ID: %v", esiResp.Corporations[0].Name, esiResp.Corporations[0].ID)
				discord.ChannelMessageSend(message.ChannelID, fmt.Sprintf(`Corporation ID: %v, found for name: %v`, esiResp.Corporations[0].ID, esiResp.Corporations[0].Name))
				break
			}

			if len(esiResp.Characters) == 1 {
				log.Infof("Character ID found for: %v, ID: %v", esiResp.Characters[0].Name, esiResp.Characters[0].ID)
				discord.ChannelMessageSend(message.ChannelID, fmt.Sprintf(`Character ID: %v, found for name: %v`, esiResp.Characters[0].ID, esiResp.Characters[0].Name))
				break
			}

			// No exact match found, attempt partial match with ESI /search
			log.Info("No exact match found, attempting ESI search")

			// TODO wildcard searches

		default:
			// don't murder the cpu
			time.Sleep(500 * time.Nanosecond)
		}
	}
	log.Debug("Exited Eve-ID lookup thread")
}
