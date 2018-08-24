package main

import (
	"github.com/bwmarrin/discordgo"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type ZKillBot struct {
	// Config Management
	viperConfig *viper.Viper

	// Logging
	log *logrus.Logger

	// Discord Methods
	discord *discordgo.Session

	// Discord message commands
	eveIDLookup chan discordCommand
}

/*
	Struct for passing commands from discord to zkill functions
*/
type discordCommand struct {
	ChannelID string
	Message   string
}

/*
   Eve Universe ID lookup and response
*/
type eveUniverseIDLookup []string
type eveUniverseIDResponse struct {
	Alliances []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"alliances"`
	Characters []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"characters"`
	Corporations []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"corporations"`
}
