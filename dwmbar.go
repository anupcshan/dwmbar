package main

import (
	"log"
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
