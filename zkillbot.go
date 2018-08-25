package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/antihax/goesi"
	"github.com/bwmarrin/discordgo"
	"github.com/gregjones/httpcache"
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
	// TODO set this to a reasonable value after testing
	viper.SetDefault("esi_max_search_requests", 200)

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

	// Init Context, Httpcache and goesi
	tCache := httpcache.NewMemoryCacheTransport()
	tCache.Transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
	httpClient := &http.Client{
		Transport: tCache,
	}
	esiClient := goesi.NewAPIClient(httpClient, "andytsnowden/zkillbot")

	// Return setup struct
	return ZKillBot{
		viperConfig: viper.GetViper(),
		log:         log,
		eveIDLookup: zkillGroupLookup,
		esiClient:   esiClient,
		ctx:         context.Background(),
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
	if strings.HasPrefix(m.Content, "!lookup") {
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
	esiClient := bot.esiClient
	discord := bot.discord
	config := bot.viperConfig

	log.Debug("Starting Eve-ID lookup thread")
	for {
		select {
		// on message do work
		case message := <-bot.eveIDLookup:
			log.Debugf("Lookup command received: %v, %v", message.Message, message.ChannelID)

			// Remove command prefix and format request
			msg := regexp.MustCompile(`!lookup\s`).ReplaceAllString(message.Message, "")

			// ESI requires at least 3 elements to search
			if len(msg) < 3 {
				log.Error("search must have at least 3 elements")
				discord.ChannelMessageSend(message.ChannelID, "Lookup requires at least 3 characters")
				return
			}

			// Wildcard search
			search, response, err := esiClient.ESI.SearchApi.GetSearch(bot.ctx, []string{"alliance", "character", "corporation"}, msg, nil)

			// Handle Err and non-200s
			if err != nil || response.StatusCode != http.StatusOK {
				log.Errorf("EVE ESI request failed code: %v, err: %v", response, err)
				discord.ChannelMessageSend(message.ChannelID, "EVE ESI error, unable to perform lookup at this time.")
				break
			}

			// Add responses and return error if greater than xx, ask for more specific search
			tLen := len(search.Alliance) + len(search.Corporation) + len(search.Character)
			if tLen > config.GetInt("esi_max_search_requests") {
				log.Info("Too many results returned by search")
				discord.ChannelMessageSend(message.ChannelID, "Too many results returned, please use more specific search phrase")
				break
			}

			// Merge slices
			var IDs []int32
			IDs = append(IDs, search.Alliance...)
			IDs = append(IDs, search.Corporation...)
			IDs = append(IDs, search.Character...)

			// Translate IDs to Strings
			idToStrings, response, err := esiClient.ESI.UniverseApi.PostUniverseNames(bot.ctx, IDs, nil)

			// Handle Err and non-200s
			if err != nil || response.StatusCode != http.StatusOK {
				log.Errorf("EVE ESI request failed code: %v, err: %v", response.StatusCode, err)
				// TODO for 400's we should return a different error message
				discord.ChannelMessageSend(message.ChannelID, "EVE ESI error, unable to perform lookup at this time.")
				break
			}

			// No results?
			if len(idToStrings) == 0 {
				log.Info("No results returned for search query")
				discord.ChannelMessageSend(message.ChannelID, "No results for lookup query")
				break
			}

			// Translate struct return into array of strings for message embed
			var alliances []string
			var corporations []string
			var characters []string
			// TODO come up with a nicer looking format, perhaps using markdown
			for _, res := range idToStrings {
				switch res.Category {
				case "alliance":
					alliances = append(alliances, fmt.Sprintf("%v - %v", res.Name, res.Id))
				case "corporation":
					corporations = append(corporations, fmt.Sprintf("%v - %v", res.Name, res.Id))
				case "character":
					characters = append(characters, fmt.Sprintf("%v - %v", res.Name, res.Id))
				}
			}

			// Build embed objects if there are results to return for that type
			var embedFields []*discordgo.MessageEmbedField
			if len(alliances) > 0 {
				embedFields = append(embedFields, &discordgo.MessageEmbedField{
					Name:   "Alliances",
					Value:  strings.Join(alliances, "\n"),
					Inline: false,
				})
			}
			if len(corporations) > 0 {
				embedFields = append(embedFields, &discordgo.MessageEmbedField{
					Name:   "Corporations",
					Value:  strings.Join(corporations, "\n"),
					Inline: false,
				})
			}
			if len(characters) > 0 {
				embedFields = append(embedFields, &discordgo.MessageEmbedField{
					Name:   "Characters",
					Value:  strings.Join(characters, "\n"),
					Inline: false,
				})
			}

			// Send final message back to discord
			_, errr := discord.ChannelMessageSendEmbed(message.ChannelID, &discordgo.MessageEmbed{
				Title:  "Lookup Results",
				Color:  0x6AA84F,
				Fields: embedFields,
			})

			if errr != nil {
				log.Errorf("Failed to send discord message: %v", err)

			}

		default:
			// don't murder the cpu
			time.Sleep(500 * time.Nanosecond)
		}
	}
	log.Debug("Exited Eve-ID lookup thread")
}
