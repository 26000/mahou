package matrix

import (
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/26000/gomatrix"
	"github.com/26000/mahou/config"
	// "github.com/syndtr/goleveldb/leveldb"
)

const (
	// TIMEOUT is the HTTP timeout.
	TIMEOUT = 0 * time.Second

	// KeepAliveTimeout is how long we keep a keep-alive socket for.
	KeepAliveTimeout = 5 * time.Second
)

// Login tries to log into a Matrix account using login and password, returning
// a Matrix client with an access token set.
func Login(bot *maConf.Login) (*gomatrix.Client, error) {
	// for requests being resent to not be sent twice.
	var trans http.RoundTripper = &http.Transport{
		DialContext: (&net.Dialer{
			KeepAlive: KeepAliveTimeout,
		}).DialContext}

	cli, _ := gomatrix.NewClientWithHTTPClient(bot.HomeServer, "", "",
		&http.Client{
			Timeout:   TIMEOUT,
			Transport: trans,
		})
	resp, err := cli.Login(&gomatrix.ReqLogin{
		Type:     "m.login.password",
		User:     bot.Localpart,
		Password: bot.Password,
		InitialDeviceDisplayName: fmt.Sprintf("mahou v%v",
			maConf.VERSION),
	})

	if err != nil {
		return cli, err
	}

	cli.SetCredentials(resp.UserID, resp.AccessToken)
	return cli, err
}

// Launch starts the Matrix module. It listens for events and processes them.
func Launch(conf *maConf.Config, wg *sync.WaitGroup) {
	defer wg.Done()

	var err error
	mxLogger := log.New(os.Stdout, "Matrix ", log.LstdFlags)
	//dbLogger := log.New(os.Stdout, "LevelDB", log.LstdFlags)

	//db, err := leveldb.OpenFile(pack.Config.Bridge.DB, nil)
	//defer db.Close()

	//if err != nil {
	//dbLogger.Fatalf("could not intialize the DB: %v\n", err)
	//}

	var mx *gomatrix.Client
	if conf.Login.Password != "" {
		mx, err = Login(conf.Login)
		if err != nil {
			mxLogger.Fatalf("could not log in: %v\n", err)
		}

		mxLogger.Println("got an access token, writing the config")
		conf.UpdateCredentials(mx.UserID, mx.AccessToken,
			conf.Login.HomeServer)
		mxLogger.Println("config updated, password redacted")
	} else {
		mxLogger.Println("no password supplied, trying access token...")

		// for requests being resent to not be sent twice.
		var trans http.RoundTripper = &http.Transport{
			DialContext: (&net.Dialer{
				KeepAlive: KeepAliveTimeout,
			}).DialContext}

		mx, err = gomatrix.NewClientWithHTTPClient(
			conf.Login.HomeServer,
			conf.Login.UserID, conf.Login.AccessToken,
			&http.Client{
				Timeout:   TIMEOUT,
				Transport: trans,
			})
		if err != nil {
			mxLogger.Fatalf("could not log in: %v\n", err)
		}
	}

	sendCh := make(chan event, 1000)
	go sendEvents(sendCh, mx, mxLogger)

	syncer := mx.Syncer.(*gomatrix.DefaultSyncer)
	syncer.OnEventType("m.room.message", func(ev *gomatrix.Event) {
		mxLogger.Println("incoming message: ", ev)
	})

	syncer.OnEventType("m.call.invite", func(ev *gomatrix.Event) {
		callID, ok := ev.Content["call_id"].(string)
		if !ok {
			// TODO logme
			return
		}

		mxLogger.Printf("rejecting voice %v from %v\n", callID,
			ev.Sender)
		sendCh <- event{ev.RoomID, "m.call.hangup", struct {
			CallID  string `json:"call_id"`
			Version int    `json:"version"`
		}{callID, 0}, "",
		}
	})

	syncer.OnEventType("m.room.member", func(ev *gomatrix.Event) {
		if *ev.StateKey == conf.Login.UserID {
			mxLogger.Printf("trying to join room %v (invited by %v)\n",
				ev.RoomID, ev.Sender)
			_, err := mx.JoinRoom(ev.RoomID, "", nil)
			if err != nil {
				mxLogger.Printf("unable to join room: %v\n",
					err)
			}
		}
	})

	wg.Add(1)
	go func(mx *gomatrix.Client, mxLogger *log.Logger) {
		for {
			if err := mx.Sync(); err != nil {
				mxLogger.Printf("failed to sync: %v\n", err)
				time.Sleep(time.Duration(4) * time.Second)
			}
		}
	}(mx, mxLogger)
}

// queueText sends a text message to the event queue to be sent in
// chronological order.
func queueText(sendCh chan event, room, text string) {
	sendCh <- event{
		RoomID:  room,
		Type:    "m.room.message",
		Content: gomatrix.TextMessage{MsgType: "m.text", Body: text}}
}

// sendEvents tries to send events from sendCh, retrying with time
// increasing exponentially on fails. It also preserves the chronological order.
// TODO: redact token from the errorlogs.
func sendEvents(sendCh chan event, mx *gomatrix.Client,
	logger *log.Logger) {
	for message := range sendCh {
		var err error

		if message.StateKey == "" {
			_, err = mx.SendMessageEvent(message.RoomID,
				message.Type, message.Content)
		} else {
			_, err = mx.SendStateEvent(message.RoomID, message.Type,
				message.StateKey, message.Content)
		}

		if err == nil {
			continue
		}

		if !errorRetriable(err) {
			logger.Printf("%v, not retrying", err)
			continue
		}

		for i := 0; i < 9 && err != nil; i += 1 {
			delay := math.Pow(2, float64(i))
			logger.Printf("%v, retrying in %v seconds...\n",
				err, delay)
			time.Sleep(time.Duration(delay) * time.Second)
			if message.StateKey == "" {
				_, err = mx.SendMessageEvent(message.RoomID,
					message.Type, message.Content)
			} else {
				_, err = mx.SendStateEvent(message.RoomID,
					message.Type, message.StateKey,
					message.Content)
			}
		}

		if err != nil {
			logger.Printf("failed to send event: %v to %v: %v\n",
				message.Type, message.RoomID, err)
		}
	}
}

// errorRetriable finds out if we should retry sending an event which has failed
// with this error or not.
func errorRetriable(err error) bool {
	HTTPErr, ok := err.(gomatrix.HTTPError)
	if ok == false {
		return true
	}

	code := HTTPErr.Code
	if code < 500 && code >= 400 && code != 429 {
		return false
	}

	if code <= 500 && code != 502 {
		return false
	}
	return true
}

// event describes an event to be sent when network is available.
type event struct {
	RoomID   string
	Type     string
	Content  interface{}
	StateKey string
}
