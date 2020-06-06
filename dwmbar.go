package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xproto"
)

type block func() <-chan string

func getTimeStr() <-chan string {
	ch := make(chan string)
	go func() {
		for {
			ch <- time.Now().Format("02 Jan 15:04")
			time.Sleep(time.Second)
		}
	}()

	return ch
}

func getBatteryStr() <-chan string {
	ch := make(chan string)
	go func() {
		for {
			ch <- batteryStr()
			time.Sleep(5 * time.Second)
		}
	}()

	return ch
}

func formatDuration(d time.Duration) string {
	hrs := d / time.Hour
	d -= hrs * time.Hour
	return fmt.Sprintf("%02d:%02d", hrs, d/time.Minute)
}

func batteryStr() string {
	capacityBytes, err := ioutil.ReadFile("/sys/class/power_supply/BAT0/capacity")
	if err != nil {
		return err.Error()
	}

	chargeBytes, err := ioutil.ReadFile("/sys/class/power_supply/BAT0/charge_now")
	if err != nil {
		return err.Error()
	}

	chargeFullBytes, err := ioutil.ReadFile("/sys/class/power_supply/BAT0/charge_full")
	if err != nil {
		return err.Error()
	}

	currentBytes, err := ioutil.ReadFile("/sys/class/power_supply/BAT0/current_now")
	if err != nil {
		return err.Error()
	}

	statusBytes, err := ioutil.ReadFile("/sys/class/power_supply/BAT0/status")
	if err != nil {
		return err.Error()
	}

	capacity, _ := strconv.Atoi(strings.TrimSpace(string(capacityBytes)))
	charge, _ := strconv.Atoi(strings.TrimSpace(string(chargeBytes)))
	chargeFull, _ := strconv.Atoi(strings.TrimSpace(string(chargeFullBytes)))
	current, _ := strconv.Atoi(strings.TrimSpace(string(currentBytes)))
	status := strings.TrimSpace(string(statusBytes))

	switch status {
	case "Discharging":
		timeRemaining := time.Duration((60*charge)/current) * time.Minute
		return fmt.Sprintf("BAT: [D] %d%% (%s)", capacity, formatDuration(timeRemaining))
	case "Charging":
		if current == 0 {
			return fmt.Sprintf("BAT: [F] %d%%", capacity)
		}
		timeRemaining := time.Duration((60*(chargeFull-charge))/current) * time.Minute
		return fmt.Sprintf("BAT: [C] %d%% (%s)", capacity, formatDuration(timeRemaining))
	case "Full":
		return fmt.Sprintf("BAT: [F] %d%%", capacity)
	}
	return "BAT: Unknown"
}

type posStr struct {
	str string
	pos int
}

func genStr(blocks []block) <-chan string {
	subStrs := make([]string, len(blocks))
	outCh := make(chan string)
	inCh := make(chan posStr)

	for i := range blocks {
		go func(i int, blk block) {
			blockCh := blk()

			for {
				str := <-blockCh
				inCh <- posStr{
					str: str,
					pos: i,
				}
			}
		}(i, blocks[i])
	}

	go func() {
		for update := range inCh {
			subStrs[update.pos] = update.str
			outCh <- strings.Join(subStrs, " | ")
		}
	}()

	return outCh
}

func main() {
	blocks := []block{
		getBatteryStr,
		getTimeStr,
	}

	err := drawBar(genStr(blocks))

	if err != nil {
		log.Fatal(err)
	}
}

func drawBar(barCh <-chan string) error {
	X, err := xgb.NewConn()
	if err != nil {
		return err
	}

	setup := xproto.Setup(X)
	root := setup.DefaultScreen(X).Root // Assuming the root window id doesn't change after launch

	for {
		wname := <-barCh

		err = xproto.ChangePropertyChecked(X, xproto.PropModeReplace, root, xproto.AtomWmName,
			xproto.AtomString, 8, uint32(len(wname)), []byte(wname)).Check()
		if err != nil {
			return err
		}
	}
}
