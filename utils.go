package main

import (
	"fmt"
	"os"

	"math"
	"math/rand"
	"time"

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

// Backoff is a time.Duration counter. It starts at Min.  After every call to Duration()
// it is multiplied by Factor.  It is capped at Max. It returns to Min on every call to Reset().
type Backoff struct {
	attempts int
	//Factor is the multiplying factor for each increment step
	Factor float64
	//Jitter eases contention by randomizing backoff steps
	Jitter bool
	//Min and Max are the minimum and maximum values of the counter
	Min, Max time.Duration
}

// Attempts returns the current attempt count
func (b *Backoff) Attempts() int {
	return b.attempts
}

// Duration returns the current value of the counter and then multiplies it by Factor
func (b *Backoff) Duration() time.Duration {
	//Zero-values are nonsensical, so we use
	//them to apply defaults
	if b.Min == 0 {
		b.Min = 100 * time.Millisecond
	}
	if b.Max == 0 {
		b.Max = 10 * time.Second
	}
	if b.Factor == 0 {
		b.Factor = 2
	}

	//calculate this duration
	dur := float64(b.Min) * math.Pow(b.Factor, float64(b.attempts))
	if b.Jitter == true {
		dur = rand.Float64()*(dur-float64(b.Min)) + float64(b.Min)
	}

	// if we're at the cap, just use it
	if dur > float64(b.Max) {
		return b.Max
	}

	b.attempts++

	//return as a time.Duration
	return time.Duration(dur)
}

//Reset resets the current value of the counter back to Min
func (b *Backoff) Reset() {
	b.attempts = 0
}
