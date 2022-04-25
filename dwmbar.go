package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xproto"
)

const (
	batteryPath = "/sys/class/power_supply"
	netDevPath  = "/proc/net/dev"
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

type ifaceState struct {
	rxBytes int
	txBytes int
}

func (i ifaceState) Sub(j ifaceState, dur time.Duration) ifaceState {
	return ifaceState{
		rxBytes: int(float64(i.rxBytes-j.rxBytes) / dur.Seconds()),
		txBytes: int(float64(i.txBytes-j.txBytes) / dur.Seconds()),
	}
}

func humanize(bps int) string {
	suffix := ""

	fbps := float64(8 * bps)

	for fbps > 1000 {
		switch suffix {
		case "":
			suffix = "k"
		case "k":
			suffix = "m"
		case "m":
			suffix = "g"
		case "g":
			suffix = "t"
		default:
			suffix = "WTF"
		}

		fbps /= 1000.0
	}
	return fmt.Sprintf("%.1f%s", fbps, suffix)
}

func (i ifaceState) String() string {
	return fmt.Sprintf("NET: %s↓ %s↑", humanize(i.rxBytes), humanize(i.txBytes))
}

func getNetStr() <-chan string {
	ch := make(chan string)
	var prevState *ifaceState
	var prevTs time.Time

	go func() {
		for {
			nextState, err := netStr()
			if err != nil {
				log.Println(err)
				continue
			}
			if prevState != nil {
				ch <- nextState.Sub(*prevState, time.Now().Sub(prevTs)).String()
			}
			prevState = nextState
			prevTs = time.Now()
			time.Sleep(2 * time.Second)
		}
	}()

	return ch
}

func netStr() (*ifaceState, error) {
	f, err := os.Open(netDevPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Skip headers - first 2 lines
	scanner.Scan()
	scanner.Scan()

	var ifStates ifaceState

	for scanner.Scan() {
		var iface string
		var rxBytes, txBytes int
		var unused int
		// Inter-|   Receive                                                |  Transmit
		//  face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
		//     lo: 1143585   14403    0    0    0     0          0         0  1143585   14403    0    0    0     0       0          0
		if _, err := fmt.Sscanf(
			scanner.Text(),
			"%s %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d %d",
			&iface, &rxBytes, &unused, &unused, &unused, &unused, &unused, &unused, &unused,
			&txBytes, &unused, &unused, &unused, &unused, &unused, &unused, &unused,
		); err != nil {
			return nil, err
		}

		// Filter out lo and other not relevant interfaces
		if strings.HasPrefix(iface, "wlp") ||
			strings.HasPrefix(iface, "enx") || strings.HasPrefix(iface, "eth") {
			ifStates.rxBytes += rxBytes
			ifStates.txBytes += txBytes
		}
	}

	return &ifStates, nil
}

func formatDuration(d time.Duration) string {
	hrs := d / time.Hour
	d -= hrs * time.Hour
	return fmt.Sprintf("%02d:%02d", hrs, d/time.Minute)
}

func sysfsInt(path string) (int, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func sysfsStr(path string) (string, error) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(b)), nil
}

func batteryStr() string {
	capacity, err := sysfsInt(path.Join(batteryPath, "BAT0", "capacity"))
	if err != nil {
		log.Println(err)
		return ""
	}

	chargeFull, err := sysfsInt(path.Join(batteryPath, "BAT0", "charge_full"))
	if err != nil {
		log.Println(err)
		return ""
	}

	charge, err := sysfsInt(path.Join(batteryPath, "BAT0", "charge_now"))
	if err != nil {
		log.Println(err)
		return ""
	}

	current, err := sysfsInt(path.Join(batteryPath, "BAT0", "current_now"))
	if err != nil {
		log.Println(err)
		return ""
	}

	status, err := sysfsStr(path.Join(batteryPath, "BAT0", "status"))
	if err != nil {
		log.Println(err)
		return ""
	}

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

func getPlayerStr() <-chan string {
	pr, pw := io.Pipe()
	cmd := exec.Command("playerctl", "metadata", "--format", "{{emoji(status)}} {{ artist }} - {{ title }}", "--follow")
	cmd.Stdout = pw

	if err := cmd.Start(); err != nil {
		return nil
	}

	ch := make(chan string)

	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			ch <- scanner.Text()
		}
	}()

	return ch
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
		getNetStr,
		getPlayerStr,
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
