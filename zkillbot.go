package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/antihax/goesi"
	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"
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
	viper.SetDefault("esi_max_search_requests_soft", 10)

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

	// TODO come up with good channel sizes
	// Init channels
	eveIDLookupChan := make(chan discordCommand, 5)
	zkillMessageChan := make(chan string, 5)
	zkillTrackingChan := make(chan discordCommand, 5)

	// Subscription data structures
	var dataStorage DataStorage
	dataStorage.SubMap = make(map[int]map[string]*subscriptionData, 10)
	dataStorage.ChannelMap = make(map[string]map[int]*subscriptionData, 10)

	// Read in dataStorage from viper
	// TODO viper can't natively do this, need a function

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
		ctx:         context.Background(),
		viperConfig: viper.GetViper(),
		log:         log,

		eveIDLookup:   eveIDLookupChan,
		zkillMessage:  zkillMessageChan,
		zkillTracking: zkillTrackingChan,

		esiClient: esiClient,

		dataStorage: &dataStorage,
	}
}

/*
	Connect to discord and set object
*/
func (bot *ZKillBot) connectDiscord() {
	log := bot.log
	discordToken := bot.viperConfig.GetString("discord_bot_token")
	log.Info("Starting websocket connection to Discord")

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
	Connect to zkill websocket
*/
func (bot *ZKillBot) connectzKillboardWS() {
	log := bot.log
	log.Info("Starting websocket connection to zKillboard")

	// Lock all other access until we set
	bot.mux.Lock()

	// Connect to websocket
	conn, _, err := websocket.DefaultDialer.Dial("wss://zkillboard.com:2096", nil)
	if err != nil {
		log.Errorf("Failed to connect to zkill wss: %v", err)
	}

	// Set method websocket
	bot.zKillboard = conn
	bot.mux.Unlock()
}

/*
	Start routines for keepalive and message receiver
*/
func (bot ZKillBot) consumezKillboardWS(cContext context.Context) {
	log := bot.log
	conn := bot.zKillboard

	// Listen for messages
	go func(ctx context.Context) {
		log.Debugf("Starting zKillboard websocket receiver thread")
		for {
			select {
			case <-ctx.Done():
				return
			default:
				_, message, err := conn.ReadMessage()
				if err != nil {
					log.Errorf("Error while reading from WS, reconnecting: %v", err)

					// On error we disconnect and reconnect
					bot.mux.Lock()
					bot.zKillboard.Close()
					bot.mux.Unlock()
					// connect
					bot.connectzKillboardWS()

					// Delay a few seconds
					time.Sleep(5 * time.Second)
				} else {
					// Put message into channel
					bot.zkillMessage <- string(message)
				}
			}
		}
	}(cContext)

	// keep alive
	go func(ctx context.Context) {
		log.Debug("Starting zkillboard websocket keepalive thread")
		timer := time.NewTicker(30 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				ping := bot.zKillboard.WriteMessage(websocket.PingMessage, []byte("PING"))
				if ping != nil {
					log.Errorf("failed to send PING, reconnecting: %v", ping)

					// On error we disconnect and reconnect
					bot.mux.Lock()
					bot.zKillboard.Close()
					bot.mux.Unlock()
					// connect
					bot.connectzKillboardWS()

					// Delay a few seconds
					time.Sleep(5 * time.Second)
				}
			}
		}
	}(cContext)

	// todo on start-up we need to subscribe to the streams defined in the config
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

	// Handle Track
	if strings.HasPrefix(m.Content, "!track") {
		// throw into command chan
		bot.zkillTracking <- discordCommand{
			ChannelID: m.ChannelID,
			Message:   m.Content,
		}
		return
	}

	// TODO more commands!
}

/*
	Message processor for zKillboard websocket message
*/
func (bot ZKillBot) zKillboardReceive(cContext context.Context) {
	log := bot.log

	log.Debugf("Starting zKillboardReceive thread")
	for {
		select {
		// cancel cleanly
		case <-cContext.Done():
			return
			// on message do work
		case message := <-bot.zkillMessage:
			log.Debugf("zkillmessage: %v", message)

			kill := KillSummary{}
			err := json.Unmarshal([]byte(message), &kill)
			if err == nil {
				bot.discord.ChannelMessageSend("482251762863177741", kill.URL)
			}

			// TODO for each kill we need to iterate over the killed, people who kills and look for a match in a reference map/slice/something
			// TODO if that matches one of the IDs we're watching it needs to be sent to the correct channel

			// for now yell for every kill
			//bot.discord.ChannelMessageSend("482251762863177741", "a kill happened somewhere")

		default:
			// don't murder the cpu
			time.Sleep(500 * time.Nanosecond)
		}
	}
	log.Debugf("Exited zKillboardReceive thread")
}

/*
   Handle adding and removing ID subscriptions from the zKillboard websocket
*/
func (bot ZKillBot) zKillboardTrack(cContext context.Context) {
	log := bot.log
	discord := bot.discord

	// sub-command patterns
	addID := regexp.MustCompile(`!track\s(?P<first_char>\d+)\s?(\d+)?`) // !track <eve_id> | !track <eve_id> <min_value>
	removeID := regexp.MustCompile(`!track\sremove\s?(\d+)?`)           // !track remove | !track remove <eve_id)

	log.Debugf("Starting zKillboardTrack thread")
	for {
		select {
		// cancel cleanly
		case <-cContext.Done():
			return
			// on message do work
		case message := <-bot.zkillTracking:
			// switch over sub-commands
			switch {
			case addID.MatchString(message.Message):
				log.Info("Add ID sub-command")

				// Pull out ID and optionally min filter value
				id, err := strconv.Atoi(addID.FindAllStringSubmatch(message.Message, -1)[0][1]) // This is the first capture group from the first match and converts to int
				if err != nil {
					discord.ChannelMessageSend(message.ChannelID, "ID to add must be numeric")
					break
				}
				minVal, err := strconv.Atoi(addID.FindAllStringSubmatch(message.Message, -1)[0][2]) // This is the second capture group from the first match and converts to int
				if err != nil {
					// on fail we just default to 0
					minVal = 0
				}

				// Handle Add Request, errors handled internally
				bot.zkillboardAddID(message.ChannelID, id, minVal)
				break

			case removeID.MatchString(message.Message):
				log.Info("Remove sub-command")
				// TODO implement remove function
				break

			default:
				log.Debugf("Invalid !track sub-command")
				// TODO make help message
				discord.ChannelMessageSend(message.ChannelID, "Invalid !track command, <help text here>")
				break
			}
		default:
			// don't murder the cpu
			time.Sleep(500 * time.Nanosecond)
		}
	}
	log.Debugf("Exited zKillboardTrack thread")
}

/*
   Given ID and min_value add ID to tracking and subscribe on zkillboard websocket connection
*/
func (bot *ZKillBot) zkillboardAddID(channelID string, eveID int, minVal int) {
	log := bot.log
	discord := bot.discord
	esiClient := bot.esiClient

	// Test if exists first
	if _, ok := bot.dataStorage.SubMap[eveID][channelID]; ok {
		log.Error("ID already exists for channel")
		discord.ChannelMessageSend(channelID, fmt.Sprintf("EVE ID: %v has already been added for this channel", eveID))
		return
	}

	// Get Name of Type from eveID
	eveID32 := []int32{int32(eveID)} // int to single slice of int32
	search, response, err := esiClient.ESI.UniverseApi.PostUniverseNames(bot.ctx, eveID32, nil)
	if err != nil || response.StatusCode != 200 {
		log.Errorf("Failed to perform typeID lookup, err: %v", err)
		discord.ChannelMessageSend(channelID, "EVE ESI error, unable to find match for ID")
		return
	}

	// We only care about the first result, error if somehow this does not exist
	if len(search[0].Category) == 0 {
		// TODO better error message
		log.Errorf("Failed to perform typeID lookup, err: %v", err)
		discord.ChannelMessageSend(channelID, "EVE ESI error, unable to find match for ID")
		return
	}

	// Init and assign data
	bot.mux.Lock()
	// init if not existing
	if _, ok := bot.dataStorage.SubMap[eveID]; !ok {
		bot.dataStorage.SubMap[eveID] = map[string]*subscriptionData{}
	}
	bot.dataStorage.SubMap[eveID] = map[string]*subscriptionData{}
	bot.dataStorage.SubMap[eveID][channelID] = &subscriptionData{
		DiscordChannelID: channelID,
		EveID:            eveID,
		EveName:          search[0].Name,
		EveCategory:      search[0].Category,
		MinVal:           minVal,
	}

	// init if not existing
	if _, ok := bot.dataStorage.ChannelMap[channelID]; !ok {
		bot.dataStorage.ChannelMap[channelID] = map[int]*subscriptionData{}
	}
	bot.dataStorage.ChannelMap[channelID][eveID] = &subscriptionData{
		DiscordChannelID: channelID,
		EveID:            eveID,
		EveName:          search[0].Name,
		EveCategory:      search[0].Category,
		MinVal:           minVal,
	}
	bot.mux.Unlock()

	// Write out config
	bot.viperConfig.Set("dataStorage", &bot.dataStorage)
	cfgerr := bot.viperConfig.WriteConfig()
	if cfgerr != nil {
		log.Errorf("Failed to write config file: %v", cfgerr)
		discord.ChannelMessageSend(channelID, "Failed to add ID to channel due to internal error")
		return
	}

	// Subscribe to channel
	subErr := bot.zKillboard.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"action":"sub","channel":"%v:%v"}`, search[0].Category, eveID)))
	if subErr != nil {
		log.Errorf("Failed to subscribe to killstream: %v", err)
		discord.ChannelMessageSend(channelID, "Unable to subscribe to killstream due to error")
		return
	}

	log.Infof("Eve ID: %v added to channel", eveID)
	discord.ChannelMessageSend(channelID, fmt.Sprintf("Eve ID: %v (%v: %v) added to channel with minimum value filter of: %v", eveID, search[0].Category, search[0].Name, minVal))
	return
}

/*
	For a string look up the eve ID and type
	The ID will be needed when creating the websocket connection to zkill
*/
func (bot ZKillBot) eveIDLookupCmd(cContext context.Context) {
	log := bot.log
	esiClient := bot.esiClient
	discord := bot.discord
	config := bot.viperConfig

	log.Debug("Starting Eve-ID lookup thread")
	for {
		select {
		// cancel cleanly
		case <-cContext.Done():
			return
		// on message do work
		case message := <-bot.eveIDLookup:
			log.Debugf("Lookup command received: %v, %v", message.Message, message.ChannelID)

			// Remove command prefix and format request
			msg := regexp.MustCompile(`!lookup\s`).ReplaceAllString(message.Message, "")

			// ESI requires at least 3 elements to search
			if len(msg) < 3 {
				log.Error("search must have at least 3 elements")
				discord.ChannelMessageSend(message.ChannelID, "Lookup requires at least 3 characters")
				break
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
			// If we exceed the stop cap we drop results, this is due to discord's max message length
			resCount := 0
			resMax := viper.GetInt("esi_max_search_requests_soft")
			// TODO come up with a nicer looking format, perhaps using markdown
			for _, res := range idToStrings {
				switch res.Category {
				case "alliance":
					if resCount < resMax {
						alliances = append(alliances, fmt.Sprintf("%v - %v", res.Name, res.Id))
						resCount++
					}

				case "corporation":
					if resCount < resMax {
						corporations = append(corporations, fmt.Sprintf("%v - %v", res.Name, res.Id))
						resCount++
					}

				case "character":
					if resCount < resMax {
						characters = append(characters, fmt.Sprintf("%v - %v", res.Name, res.Id))
						resCount++
					}
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

			// Warn the user if their search result has been limited dur to size
			desc := ""
			if resCount >= resMax {
				desc = fmt.Sprintf("Only %v of the %v results shown, please use a more specific lookup phrase", resCount, len(idToStrings))
			}

			// Send final message back to discord
			_, errr := discord.ChannelMessageSendEmbed(message.ChannelID, &discordgo.MessageEmbed{
				Title:       "Lookup Results",
				Color:       0x6AA84F,
				Fields:      embedFields,
				Description: desc,
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
