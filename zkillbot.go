package main

import (
	"bytes"
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
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// NewZKillBot is the initialization function of the bot.
// It reads or creates the configuration file via Viper, setups up channels, and starts logging.
func NewZKillBot() *ZKillBot {
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
	dataStorage = loadViperData(viper.Get("datastorage"), log)

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
	return &ZKillBot{
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

// connectDiscord creates a websocket connection to the Discord API given a bot_token
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

// connectzKillboardWS creates a websocket connection to the zKillboard API
// This function will attempt to maintain the websocket connection and reconnect if it fails using a backoff
func (bot *ZKillBot) connectzKillboardWS() {
	log := bot.log
	boff := Backoff{
		Min:    500 * time.Millisecond,
		Max:    5 * time.Minute,
		Factor: 2,
		Jitter: true,
	}

	// Forever keep the connection alive
	for {
		conn, _, err := websocket.DefaultDialer.Dial("wss://zkillboard.com:2096", nil)

		if err != nil {
			dur := boff.Duration()
			log.Warnf("zkill reconnection %d failed: %s", boff.Attempts(), err)
			log.Warnln(" -> reconnecting in", dur)
			time.Sleep(dur)
			continue
		}
		log.Info("Connected to zKillboard Websocket")

		// reset backoff once successfully reconnected
		boff.Reset()

		bot.mux.Lock()
		// set conn for everyone
		if bot.zKillboard != nil {
			bot.zKillboard.Close()
			bot.zKillboard = nil
		}
		bot.zKillboard = conn
		bot.mux.Unlock()

		log.Errorf("Connection before resub: %s", bot.zKillboard.UnderlyingConn().LocalAddr().String())
		// Automatically connect to any saved subscriptions
		for _, subs := range bot.dataStorage.SubMap {
			// range over subs
			for _, subData := range subs {
				// build sub payload and send
				subString := []byte(fmt.Sprintf(`{"action":"sub","channel":"%v:%v"}`, subData.EveCategory, subData.EveID))
				err := conn.WriteMessage(websocket.TextMessage, subString)
				if err != nil {
					log.Errorf("Failed to subscribe to killstream: %v", err)
				} else {
					log.Debugf("subscribed to killstream for id: %v, name: %v", subData.EveID, subData.EveName)
				}
			}
		}

		// subscribe to zkillboard's public channel since they don't response to websocket PINGs
		err = bot.zKillboard.WriteMessage(websocket.TextMessage, []byte(`{"action":"sub","channel":"public"}`))
		if err != nil {
			log.Errorf("Failed to sub to public status")
			break
		}

		// Listen for messages
		for {
			// set a deadline so ReadMessage will timeout eventually
			// normally the public channel will send a message every 15 seconds, if not there's a good chance the connection is dead
			conn.SetReadDeadline(time.Now().Add(time.Second * 30))
			_, message, err := conn.ReadMessage()
			if err != nil {
				// log error and exit for, this will trigger a new connection
				log.Errorf("Error while reading from WS, reconnecting: %v", err)
				break
				// trigger context.cancel to kill keepalive thread
			} else {
				// Put message into channel
				bot.zkillMessage <- string(message)
			}

		}

		// Close connection
		bot.zKillboard.Close()
	}
}

// discordReceive is a callback function that executes whenever a websocket message is received from Discord
// Initial filtering and routing of the commands occurs here. each command will have a unique channel and processing thread
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

// zKillboardReceive is a work in progress
//
// Current we accept messages off the bot.zkillMessage channel and print them directly to a testing discord channel
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
			// TODO handle public info and store for later use

			kill := KillSummary{}
			err := json.Unmarshal([]byte(message), &kill)
			if err == nil {
				bot.discord.ChannelMessageSend("482251762863177741", kill.URL)
			}

			// TODO for each kill we need to iterate over the killed, people who kills and look for a match in a reference map/slice/something
			// TODO if that matches one of the IDs we're watching it needs to be sent to the correct channel

		default:
			// don't murder the cpu
			time.Sleep(500 * time.Nanosecond)
		}
	}
	log.Debugf("Exited zKillboardReceive thread")
}

// zKillboardTrack handles subscription requests from discord commands
//
// We accept commands !track <eve_id> <min_value> and !track remove <eve_id> as commands here
// If no sub-command is provided a contextual help will be returned TODO
func (bot *ZKillBot) zKillboardTrack(cContext context.Context) {
	log := bot.log
	discord := bot.discord

	help := `Valid commands:
!track <eve_id>              - Add a eve ID to tracking
!track <eve_id> <min_value>  - Add a eve ID to tracking with a minimum isk filter
!track remove <eve_id>       - Remove a eve ID from tracking
!track remove                - Removes all ID from tracking
!track list                  - List all tracked IDs and their names/types`

	// sub-command patterns
	addID := regexp.MustCompile(`!track\s(?P<first_char>\d+)\s?(\d+)?`) // !track <eve_id> | !track <eve_id> <min_value>
	removeID := regexp.MustCompile(`!track\sremove\s?(\d+)?`)           // !track remove | !track remove <eve_id)
	listID := regexp.MustCompile(`!track\slist.*?`)                     // !track list

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

				// Handle Add Request
				bot.zkillboardAddID(message.ChannelID, id, minVal)
				break

			case removeID.MatchString(message.Message):
				log.Info("Remove sub-command")

				// Pull out ID and optionally min filter value
				id, err := strconv.Atoi(addID.FindAllStringSubmatch(message.Message, -1)[0][1]) // This is the first capture group from the first match and converts to int
				if err != nil {
					discord.ChannelMessageSend(message.ChannelID, "ID to add must be numeric")
					break
				}

				// Handle Remove Request
				bot.zkillboardRemoveID(message.ChannelID, id)
				break

			case listID.MatchString(message.Message):
				log.Info("List sub-command")

				// Handle List Request
				bot.zkillboardListIDs(message.ChannelID)
				break

			default:
				log.Debugf("Invalid !track sub-command")
				// ``` wrapper tells discord to use a code block
				discord.ChannelMessageSend(message.ChannelID, "Invalid !track command, ```"+help+"```")
				break
			}
		default:
			// don't murder the cpu
			time.Sleep(500 * time.Nanosecond)
		}
	}
	log.Debugf("Exited zKillboardTrack thread")
}

// zkillboardAddID handles adding the requested ID to the mapping struct and sending the subscription command to the zkillboard websocket.
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
	log.Errorf("Connection before write: %s", bot.zKillboard.UnderlyingConn().LocalAddr().String())
	subErr := bot.zKillboard.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"action":"sub","channel":"%v:%v"}`, search[0].Category, eveID)))
	if subErr != nil {
		log.Errorf("Failed to subscribe to killstream: %v", subErr)
		discord.ChannelMessageSend(channelID, "Unable to subscribe to killstream due to error")
		return
	}

	log.Infof("Eve ID: %v added to channel", eveID)
	discord.ChannelMessageSend(channelID, fmt.Sprintf("Eve ID: %v (%v: %v) added to channel with minimum value filter of: %v", eveID, search[0].Category, search[0].Name, minVal))
	return
}

// zkillboardRemoveID handles removing a ID from subscription and the internal mapping
func (bot *ZKillBot) zkillboardRemoveID(channelID string, eveID int) {
	//_ := bot.log
	//_ := bot.discord
	//_ := bot.esiClient

	// TODO check if currently subscribed
	// check if multiple channels share a subscription
	// - if yes don't unsubscribe but remove sub->channel mapping
	// - if no unsubscribe and remove mapping
}

// zkillboardListIDs lists all ID's currently being tracked for the channel by zkillbot
func (bot ZKillBot) zkillboardListIDs(channelID string) {
	log := bot.log
	discord := bot.discord

	// Does the channel have any IDs being tracked?
	if len(bot.dataStorage.ChannelMap[channelID]) == 0 {
		log.Infof("List command for channelID %v fails due to no tracked IDs", channelID)
		discord.ChannelMessageSend(channelID, "Channel currently has no tracked ID, use the !track command to add")
		return
	}

	var data [][]string

	// Iterate over ids
	for _, IDs := range bot.dataStorage.ChannelMap[channelID] {
		data = append(data, []string{
			strconv.Itoa(IDs.EveID),
			strings.Title(IDs.EveCategory),
			IDs.EveName,
			strconv.Itoa(IDs.MinVal),
		})
	}

	// Take data from map to write it into a nice looking spaced table
	buf := new(bytes.Buffer)
	table := tablewriter.NewWriter(buf)
	table.SetHeader([]string{"Eve-ID", "Type", "Name", "Min Amount"})
	table.SetBorders(tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false})
	table.SetCenterSeparator("|")
	table.AppendBulk(data) // Add Bulk Data
	table.Render()

	// send to discord as code block
	discord.ChannelMessageSend(channelID, "```"+buf.String()+"```")
}

// eveIDLookupCmd handles lookup requests from discord commands
//
// Given a string from discord we search the EVE API via ESI and return a limited amount of typed results
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
