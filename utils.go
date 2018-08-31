package main

import (
	"fmt"
	"os"

	"github.com/mitchellh/mapstructure"
	"github.com/onrik/logrus/filename"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// ConfigureLogging takes a Viper object and initializes the logging methods.
func ConfigureLogging(viper *viper.Viper) (logger *log.Logger) {
	// Parse log level
	level, err := log.ParseLevel(viper.GetString("log_level"))
	if err != nil {
		fmt.Printf("Unable to parse human log level in config: %s \n", err)
		os.Exit(1)
	}
	log.SetLevel(level)

	// Log to File Support
	if viper.GetBool("log_to_file") {
		// Attempt to open/create file, fall back to console and log failure
		logFile, err := os.OpenFile(viper.GetString("log_file_path"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
		if err != nil {
			fmt.Printf("Failed to open log file path, defaulting to console: %s", err)
			log.SetOutput(os.Stdout)
		} else {
			log.SetOutput(logFile)
		}
	} else {
		log.SetOutput(os.Stdout)
	}

	// Log filename and lines
	if log.GetLevel() == log.DebugLevel {
		filenameHook := filename.NewHook()
		filenameHook.Field = "fileline"
		log.AddHook(filenameHook)
	}

	return log.StandardLogger()
}

// loadViperData takes a interface from viper.get and translates it into DataStorage via mapstructure.NewDecoder
func loadViperData(data interface{}, log *log.Logger) DataStorage {
	var dataStorage DataStorage

	// Enable Weak input so it will type convert strings<->ints
	config := &mapstructure.DecoderConfig{
		WeaklyTypedInput: true,
		Result:           &dataStorage,
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		log.Errorf("Failed to create new decoder, no previous subscriptions will be loaded: %v", err)
	}

	err = decoder.Decode(data)
	if err != nil {
		log.Errorf("Failed to decode previous subscriptions, any new requests will erase the existing config: %v", err)
	}

	return dataStorage
}
