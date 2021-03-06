package matrix

import (
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	// TODO: replace with keroserene/go-webrtc when it's merged with audio
	"github.com/26000/go-webrtc"
	"github.com/26000/gomatrix"
	"github.com/26000/mahou/config"
	// "github.com/faiface/beep/speaker"
	// "github.com/syndtr/goleveldb/leveldb"
)

const (
	// TIMEOUT is the HTTP timeout.
	TIMEOUT = 0 * time.Second

	// KeepAliveTimeout is how long we keep a keep-alive socket for.
	KeepAliveTimeout = 5 * time.Second
)

//var sp := speaker.Init

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
	var err error
	mLogger := log.New(os.Stdout, "matrix ", log.LstdFlags)
	wLogger := log.New(os.Stdout, "webRTC ", log.LstdFlags)
	uLogger := log.New(os.Stdout, " utils ", log.LstdFlags)
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
			mLogger.Fatalf("could not log in: %v\n", err)
		}

		mLogger.Println("got an access token, writing the config")
		conf.UpdateCredentials(mx.UserID, mx.AccessToken,
			conf.Login.HomeServer)
		mLogger.Println("config updated, password redacted")
	} else {
		mLogger.Println("no password supplied, trying access token")

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
			mLogger.Fatalf("could not log in: %v\n", err)
		}
	}

	sendCh := make(chan event, 1000)
	go sendEvents(sendCh, mx, mLogger)

	setupCallbacks(sendCh, wLogger,
		mLogger, uLogger, mx, wg, conf)
}

// setupCallbacks sets up the callbacks, containing the main bot logic.
// A lot of arguments passed, but i believe that there's no point in packaging
// them into a struct.
func setupCallbacks(sendCh chan event, wLogger *log.Logger,
	mLogger *log.Logger, uLogger *log.Logger, mx *gomatrix.Client,
	wg *sync.WaitGroup, conf *maConf.Config) {
	defer wg.Done()

	syncer := mx.Syncer.(*gomatrix.DefaultSyncer)

	webrtc.SetLoggingVerbosity(0)
	wConf := webrtc.NewConfiguration()

	turn, err := mx.TurnServer()
	if err != nil {
		mLogger.Printf("failed to get TURN server address: %v\n", err)
	}

	err = wConf.AddIceServer(strings.Join(turn.URIs, ","), turn.Username,
		turn.Password)
	if err != nil {
		// TODO
		wLogger.Printf("failed to add an ICE server: %v\n", err)
	}

	var calls = make(map[string]*call)

	// setup PeerConnections

	syncer.OnEventType("m.room.message", func(ev *gomatrix.Event) {
		mLogger.Println("incoming message: ", ev)
	})

	syncer.OnEventType("m.call.invite", func(ev *gomatrix.Event) {
		callID, ok := ev.Content["call_id"].(string)
		if !ok {
			uLogger.Println("failed to map call_id to string")
			return
		}

		if ev.Content["offer"] == nil {
			return
		}

		offer, ok := ev.Content["offer"].(map[string]interface{})
		if !ok {
			uLogger.Printf("failed to map offer to map (%v)\n",
				callID)
			return
		}
		sdp := offer["sdp"].(string)
		if strings.Contains(sdp, "m=video") {
			mLogger.Printf("%v videocalled us, rejecting",
				ev.Sender)
			sendCh <- event{ev.RoomID, "m.call.hangup", struct {
				CallID  string `json:"call_id"`
				Version int    `json:"version"`
			}{callID, 0}, "",
			}

			queueText(sendCh, ev.RoomID, "Sorry, videocalls "+
				"aren't supported yet!")
			return
		}

		wLogger.Printf("got SDP from %v\n", ev.Sender)
		parsedSDP := &webrtc.SessionDescription{"offer", sdp}

		if _, ok := calls[callID]; !ok {
			pc, err := setupPC(wConf, wLogger)
			if err != nil {
				wLogger.Printf("failed to create a "+
					"PeerConnection (%v): %v\n", callID, err)
				return
			}
			calls[callID] = &call{pc}
		}

		err = calls[callID].pc.SetRemoteDescription(parsedSDP)
		if err != nil {
			wLogger.Printf("failed to set remote description (%v): "+
				"%v\n", callID, err)
			return
		}

		ans, err := calls[callID].pc.CreateAnswer()
		if err != nil {
			wLogger.Printf("failed to generate answer (%v): %v\n",
				callID, err)
			return
		}

		calls[callID].pc.SetLocalDescription(ans)
		mLogger.Printf("accepting call %v from %v\n", callID,
			ev.Sender)
		sendCh <- event{ev.RoomID, "m.call.answer", struct {
			CallID  string `json:"call_id"`
			Answer  answer `json:"answer"`
			Version int    `json:"version"`
		}{callID, answer{"answer", ans.Sdp}, 0}, "",
		}
	})

	syncer.OnEventType("m.call.candidates", func(ev *gomatrix.Event) {
		cands, ok := ev.Content["candidates"].([]interface{})
		if !ok {
			uLogger.Println("failed to map ICE candidates to " +
				"an array of interfaces")
			return
		}

		callID, ok := ev.Content["call_id"].(string)
		if !ok {
			uLogger.Println("failed to map call_id to string")
			return
		}

		for _, candCoded := range cands {
			cand, ok := candCoded.(map[string]interface{})
			if !ok {
				uLogger.Printf("failed to map ICE candidate "+
					"an map[string]interface{} (%v)\n",
					callID)
				return
			}

			candidate, ok := cand["candidate"].(string)
			if !ok {
				uLogger.Printf("failed to map ICE candidate "+
					"from a map to string (%v)\n", callID)
				return
			}

			wLogger.Printf("added candidate %v (%v)\n", candidate,
				callID)

			sdpMid, ok := cand["sdpMid"].(string)
			if !ok {
				uLogger.Printf("failed to map sdpMid "+
					"from a map to string (%v)\n", callID)
				return
			}

			sdpMLineIndex, ok := cand["sdpMLineIndex"].(float64)
			if !ok {
				uLogger.Printf("failed to map sdpMLineIndex "+
					"from a map to string (%v)\n", callID)
				return
			}

			if _, ok := calls[callID]; !ok {
				pc, err := setupPC(wConf, wLogger)
				if err != nil {
					wLogger.Printf("failed to create a "+
						"PeerConnection (%v): %v\n",
						callID, err)
					return
				}
				calls[callID] = &call{pc}
			}

			err := calls[callID].pc.AddIceCandidate(
				webrtc.IceCandidate{candidate, sdpMid,
					int(sdpMLineIndex)})
			if err != nil {
				wLogger.Printf("failed to add an ICE "+
					"candidate (%v): %v\n", callID, err)
			}
		}

	})

	// TODO: close connection on hangup

	syncer.OnEventType("m.room.member", func(ev *gomatrix.Event) {
		if *ev.StateKey == conf.Login.UserID {
			mLogger.Printf("trying to join room %v (invited by %v)\n",
				ev.RoomID, ev.Sender)
			_, err := mx.JoinRoom(ev.RoomID, "", nil)
			if err != nil {
				mLogger.Printf("unable to join room: %v\n",
					err)
			}
		}
	})

	wg.Add(1)
	go func(mx *gomatrix.Client, mLogger *log.Logger) {
		for {
			if err := mx.Sync(); err != nil {
				mLogger.Printf("failed to sync: %v\n", err)
				time.Sleep(time.Duration(4) * time.Second)
			}
		}
	}(mx, mLogger)
}

// setupPC creates a new PeerConnection and adds the needed callbacks.
func setupPC(wConf *webrtc.Configuration, wLogger *log.Logger) (
	*webrtc.PeerConnection, error) {
	pc, err := webrtc.NewPeerConnection(wConf)
	if err != nil {
		return pc, err
	}

	pc.OnAddTrack = func(r *webrtc.RtpReceiver, s []*webrtc.MediaStream) {
		echo := &echo{}
		r.Track().(*webrtc.AudioTrack).AddSink(echo)
		pc.AddTrack(webrtc.NewAudioTrack("audio-echo", echo), nil)
	}

	pc.OnIceCandidateError = func() {
		wLogger.Println("an ICE candidate error occurred")
	}

	pc.OnIceConnectionStateChange = func(state webrtc.IceConnectionState) {
		wLogger.Printf("ICE state is now %v\n", state.String())
	}

	pc.OnConnectionStateChange = func(state webrtc.PeerConnectionState) {
		wLogger.Printf("connection state is now %v\n", state.String())
	}

	pc.OnSignalingStateChange = func(state webrtc.SignalingState) {
		wLogger.Printf("signaling state is now %v\n", state.String())
	}
	return pc, err
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

// answer describes a WebRTC call answer.
type answer struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type call struct {
	pc *webrtc.PeerConnection
}

type echo struct {
	sync.Mutex
	sinks []webrtc.AudioSink
}

func (e *echo) AddAudioSink(s webrtc.AudioSink) {
	e.Lock()
	defer e.Unlock()
	e.sinks = append(e.sinks, s)
}

func (e *echo) RemoveAudioSink(s webrtc.AudioSink) {
	e.Lock()
	defer e.Unlock()
	for i, s2 := range e.sinks {
		if s2 == s {
			e.sinks = append(e.sinks[:i], e.sinks[i+1:]...)
		}
	}
}

func (e *echo) OnAudioData(data [][]float64, sampleRate float64) {
	e.Lock()
	defer e.Unlock()
	for _, s := range e.sinks {
		s.OnAudioData(data, sampleRate)
	}
}
