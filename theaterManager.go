package main

import (
	"database/sql"
	"encoding/json"
	"io/ioutil"
	"net"
	"os"
	"strconv"
	"time"

	gs "github.com/HeroesAwaken/GoAwaken/GameSpy"
	log "github.com/HeroesAwaken/GoAwaken/Log"
	"github.com/HeroesAwaken/GoAwaken/core"
	"github.com/go-redis/redis"
)

// GameClient Represents a game client connected to theater
type GameClient struct {
	ip   string
	port string
}

// GameServer Represents a game server and it's data
type GameServer struct {
	ip                 string
	port               string
	intIP              string
	intPort            string
	name               string
	level              string
	activePlayers      int
	maxPlayers         int
	queueLength        int
	joiningPlayers     int
	gameMode           string
	elo                float64
	numObservers       int
	maxObservers       int
	sguid              string
	hash               string
	password           string
	ugid               string
	sType              string
	join               string
	version            string
	dataCenter         string
	serverMap          string
	armyBalance        string
	armyDistribution   string
	availSlotsNational bool
	availSlotsRoyal    bool
	avgAllyRank        float64
	avgAxisRank        float64
	serverState        string
	communityName      string
}

// Servers a hashmap of servers
var Servers = make(map[string]GameServer)

// TheaterManager Handles incoming and outgoing theater communication
type TheaterManager struct {
	name             string
	socket           *gs.Socket
	socketUDP        *gs.SocketUDP
	db               *sql.DB
	redis            *redis.Client
	eventsChannel    chan gs.SocketEvent
	eventsChannelUDP chan gs.SocketUDPEvent
	batchTicker      *time.Ticker
	stopTicker       chan bool
	gameServerGlobal *core.RedisState
}

var wantsToJoin = false
var canJoin = false
var wantsToLeaveQueue = false
var localPort = ""
var remotePort = ""
var localIP = ""
var remoteIP = ""
var userId = ""
var nickname = ""
var pid = ""

// New creates and starts a new ClientManager
func (tM *TheaterManager) New(name string, port string, db *sql.DB, redis *redis.Client) {
	var err error

	tM.socket = new(gs.Socket)
	tM.socketUDP = new(gs.SocketUDP)
	tM.db = db
	tM.redis = redis
	tM.name = name
	tM.eventsChannel, err = tM.socket.New(tM.name, port, true)
	if err != nil {
		log.Errorln(err)
	}
	tM.eventsChannelUDP, err = tM.socketUDP.New(tM.name, port, true)
	if err != nil {
		log.Errorln(err)
	}
	tM.stopTicker = make(chan bool, 1)

	tM.gameServerGlobal = new(core.RedisState)
	tM.gameServerGlobal.New(tM.redis, "gameServer-config")
	tM.gameServerGlobal.Set("Lobbies", "0")

	go tM.run()
}

func (tM *TheaterManager) run() {
	for {
		select {
		case event := <-tM.eventsChannelUDP:
			switch {
			case event.Name == "command.ECHO":
				go tM.ECHO(event)
			case event.Name == "command":
				tM.LogCommandUDP(event.Data.(*gs.CommandFESL))
				log.Debugf("UDP Got event %s: %v", event.Name, event.Data.(*gs.CommandFESL))
			default:
				log.Debugf("UDP Got event %s: %v", event.Name, event.Data)
			}
		case event := <-tM.eventsChannel:
			switch {
			case event.Name == "newClient":
				go tM.newClient(event.Data.(gs.EventNewClient))
			case event.Name == "client.command.CONN":
				go tM.CONN(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.USER":
				go tM.USER(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.LLST":
				go tM.LLST(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.GDAT":
				go tM.GDAT(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.EGAM":
				go tM.EGAM(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.ECNL":
				go tM.ECNL(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.CGAM":
				go tM.CGAM(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.UBRA":
				go tM.UBRA(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.UGAM":
				go tM.UGAM(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.EGRS":
				go tM.EGRS(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.GLST":
				go tM.GLST(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.PENT":
				go tM.PENT(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command.UPLA":
				go tM.UPLA(event.Data.(gs.EventClientFESLCommand))
			case event.Name == "client.command":
				tM.LogCommand(event.Data.(gs.EventClientFESLCommand))
				log.Debugf("Got event %s: %v", event.Name, event.Data.(gs.EventClientFESLCommand).Command)
			default:
				log.Debugf("Got event %s: %v", event.Name, event.Data)
			}
		}
	}
}

// ECHO - SHARED called like some heartbeat
func (tM *TheaterManager) ECHO(event gs.SocketUDPEvent) {
	command := event.Data.(*gs.CommandFESL)

	answerPacket := make(map[string]string)
	answerPacket["TID"] = command.Message["TID"]
	answerPacket["TXN"] = command.Message["TXN"]
	answerPacket["IP"] = event.Addr.IP.String()
	answerPacket["PORT"] = strconv.Itoa(event.Addr.Port)
	answerPacket["ERR"] = "0"
	answerPacket["TYPE"] = "1"
	err := tM.socketUDP.WriteFESL("ECHO", answerPacket, 0x0, event.Addr)
	if err != nil {
		log.Errorln(err)
	}
	tM.logAnswer("ECHO", answerPacket, 0x0)
}

// ECNL - CLIENT calls when they want to leave
func (tM *TheaterManager) ECNL(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}

	//wantsToLeaveQueue = true

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	answerPacket["GID"] = "5459"
	answerPacket["LID"] = event.Command.Message["LID"]
	event.Client.WriteFESL("ECNL", answerPacket, 0x0)
	tM.logAnswer("ECNL", answerPacket, 0x0)

	/*ap := make(map[string]string)
	ap["TID"] = "7"
	ap["GID"] = "5459"
	ap["LID"] = "1"
	event.Client.WriteFESL("ECNLmisc", ap, 0x0)
	tM.logAnswer("ECNLmisc", ap, 0x0)		*/
}

// EGAM - CLIENT called when a client wants to join a gameserver
func (tM *TheaterManager) EGAM(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	answerPacket["GID"] = "5459"
	answerPacket["LID"] = "1"

	localPort = event.Command.Message["R-INT-PORT"]
	localIP = event.Command.Message["R-INT-IP"]
	remotePort = event.Command.Message["PORT"]
	remoteIP = event.Command.Message["R-U-externalIp"]
	userId = event.Command.Message["R-U-accid"]
	log.Noteln("TEH USER IS" + userId)

	stmt, err := tM.db.Prepare("SELECT pid, nickname from revive_soldiers WHERE web_id = ? AND game='heroes'")
	defer stmt.Close()
	if err != nil {
		log.Debugln(err)
		return
	}

	err = stmt.QueryRow(event.Command.Message["R-U-accid"]).Scan(&pid, &nickname)

	wantsToJoin = true
	canJoin = false
	event.Client.WriteFESL("EGAM", answerPacket, 0x0)
	tM.logAnswer("EGAM", answerPacket, 0x0)

	//event.Client.WriteFESL("EGAM", answerPacket, 0x0)
	//tM.logAnswer("EGAM", answerPacket, 0x0)
}

// GLST - CLIENT called to get a list of game servers? Irrelevant for heroes.
func (tM *TheaterManager) GLST(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}
	log.Noteln("GLST was called")
}

// CGAM - SERVER called to create a game
func (tM *TheaterManager) CGAM(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}

	addr, ok := event.Client.IpAddr.(*net.TCPAddr)

	if !ok {
		log.Errorln("Failed turning IpAddr to net.TCPAddr")
		return
	}

	currentLobbyID := tM.gameServerGlobal.Get("Lobbies")
	gameLid, _ := strconv.Atoi(currentLobbyID)
	gameLid++

	gameServer := new(core.RedisState)
	gameServer.New(tM.redis, "gameServer-"+strconv.Itoa(gameLid))

	for index, value := range event.Command.Message {
		// Strip quotes
		if len(value) > 0 && value[0] == '"' {
			value = value[1:]
		}
		if len(value) > 0 && value[len(value)-1] == '"' {
			value = value[:len(value)-1]
		}
		gameServer.Set(index, value)
	}

	gameServer.Set("LID", strconv.Itoa(gameLid))
	gameServer.Set("IP", addr.IP.String())
	gameServer.Set("ACTIVE-PLAYERS", "0")
	gameServer.Set("QUEUE-LENGTH", "0")

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	answerPacket["MAX-PLAYERS"] = "16"
	answerPacket["EKEY"] = "O65zZ2D2A58mNrZw1hmuJw%3d%3d"
	answerPacket["UGID"] = "7eb6155c-ac70-4567-9fc4-732d56a9334a"
	answerPacket["JOIN"] = event.Command.Message["JOIN"]
	answerPacket["LID"] = "1"
	answerPacket["SECRET"] = "2587913" //
	answerPacket["J"] = "0"
	answerPacket["GID"] = "5459"
	event.Client.WriteFESL("CGAM", answerPacket, 0x0)
	tM.logAnswer("CGAM", answerPacket, 0x0)

	tM.gameServerGlobal.Set("Lobbies", strconv.Itoa(gameLid))
}

// GDAT - CLIENT called to get data about the server
func (tM *TheaterManager) GDAT(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}
	log.Noteln("GDAT WAS CALLED!")

	gameServer := new(core.RedisState)
	gameServer.New(tM.redis, "gameServer-"+event.Command.Message["LID"])

	answerPacket := make(map[string]string)

	answerPacket["TYPE"] = "G"
	answerPacket["AP"] = "15"
	answerPacket["B-U-server_port"] = "18569"
	answerPacket["PW"] = "0"
	answerPacket["B-U-avg_axis_rank"] = "800.800000"
	answerPacket["P"] = "18569"
	answerPacket["V"] = "1.02.1067.0"
	answerPacket["B-U-army_balance"] = "Balanced"
	answerPacket["B-U-avail_slots_royal"] = "yes"
	answerPacket["B-U-avail_slots_national"] = "yes"
	answerPacket["I"] = "45.77.79.240"
	answerPacket["B-U-data_center"] = "iad"
	answerPacket["HU"] = "1"
	answerPacket["B-U-army_distribution"] = "0,0,0,0,0,0,0,0,0,0,0"
	answerPacket["F"] = "1"
	answerPacket["B-maxObservers"] = "0"
	answerPacket["N"] = "[iad]gs1-test.revive.systems(45.77.79.240%3a18569)"
	answerPacket["NF"] = "0"
	answerPacket["B-version"] = "1.02.1067.0"
	answerPacket["B-U-server_ip"] = "45.77.79.240"
	answerPacket["B-U-community_name"] = "HeroesSV"
	answerPacket["B-U-percent_full"] = "0"
	answerPacket["MP"] = "16"
	answerPacket["B-U-ranked"] = "yes"
	answerPacket["B-U-easyzone"] = "no"
	answerPacket["JP"] = "0"
	answerPacket["QP"] = "0"
	answerPacket["HN"] = "gs1-test.revive.systems"
	answerPacket["GID"] = "5459"
	answerPacket["B-U-elo_rank"] = "800.800000"
	answerPacket["PL"] = "PC"
	answerPacket["B-U-server_state"] = "empty"
	answerPacket["TID"] = event.Command.Message["TID"]
	answerPacket["B-numObservers"] = "0"
	answerPacket["J"] = "O"
	answerPacket["B-U-map"] = "no_vehicles"
	answerPacket["LID"] = "1"
	answerPacket["B-U-avg_ally_rank"] = "800.800000"

	event.Client.WriteFESL("GDAT", answerPacket, 0x0)
	tM.logAnswer("GDAT", answerPacket, 0x0)

}

// LogCommandUDP log data to a debug file for further analysis
func (tM *TheaterManager) LogCommandUDP(event *gs.CommandFESL) {
	b, err := json.MarshalIndent(event.Message, "", "	")
	if err != nil {
		panic(err)
	}

	commandType := "request"

	os.MkdirAll("./commands/"+event.Query+"."+event.Message["TXN"]+"", 0777)
	err = ioutil.WriteFile("./commands/"+event.Query+"."+event.Message["TXN"]+"/"+commandType, b, 0644)
	if err != nil {
		panic(err)
	}
}

// LogCommand log data to a debug file for further analysis
func (tM *TheaterManager) LogCommand(event gs.EventClientFESLCommand) {
	b, err := json.MarshalIndent(event.Command.Message, "", "	")
	if err != nil {
		panic(err)
	}

	commandType := "request"

	os.MkdirAll("./commands/"+event.Command.Query+"."+event.Command.Message["TXN"]+"", 0777)
	err = ioutil.WriteFile("./commands/"+event.Command.Query+"."+event.Command.Message["TXN"]+"/"+commandType, b, 0644)
	if err != nil {
		panic(err)
	}
}

func (tM *TheaterManager) logAnswer(msgType string, msgContent map[string]string, msgType2 uint32) {
	b, err := json.MarshalIndent(msgContent, "", "	")
	if err != nil {
		panic(err)
	}

	commandType := "answer"

	os.MkdirAll("./commands/"+msgType+"."+msgContent["TXN"]+"", 0777)
	err = ioutil.WriteFile("./commands/"+msgType+"."+msgContent["TXN"]+"/"+commandType, b, 0644)
	if err != nil {
		panic(err)
	}
}

// LLST - CLIENT (???) unknown, potentially bookmarks
func (tM *TheaterManager) LLST(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}
	log.Noteln("LLST CALLED!")

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	answerPacket["NUM-LOBBIES"] = "1"
	event.Client.WriteFESL(event.Command.Query, answerPacket, 0x0)

	ldatPacket := make(map[string]string)
	ldatPacket["TID"] = "6"
	ldatPacket["FAVORITE-GAMES"] = "0"
	ldatPacket["FAVORITE-PLAYERS"] = "0"
	ldatPacket["LID"] = "1"
	ldatPacket["LOCALE"] = "en_US"
	ldatPacket["MAX-GAMES"] = "10000"
	ldatPacket["NAME"] = "bfwestPC02"
	ldatPacket["NUM-GAMES"] = "1"
	ldatPacket["PASSING"] = "0"
	event.Client.WriteFESL("LDAT", ldatPacket, 0x0)
	tM.logAnswer("LDAT", ldatPacket, 0x0)
}

// USER - SHARED Called to get user data about client? No idea
func (tM *TheaterManager) USER(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	answerPacket["NAME"] = nickname
	answerPacket["CID"] = userId
	event.Client.WriteFESL(event.Command.Query, answerPacket, 0x0)
	tM.logAnswer(event.Command.Query, answerPacket, 0x0)
}

// UBRA - SERVER Called to  update server data
func (tM *TheaterManager) UBRA(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	event.Client.WriteFESL(event.Command.Query, answerPacket, 0x0)
	tM.logAnswer(event.Command.Query, answerPacket, 0x0)
}

// UGAM - SERVER Called to udpate serverquery ifo
func (tM *TheaterManager) UGAM(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}

	log.Noteln("yo dis a server ")
	event.Client.State.IsServer = true

	gameServer := new(core.RedisState)
	gameServer.New(tM.redis, "gameServer-"+event.Command.Message["LID"])

	log.Noteln("Updating GameServer " + event.Command.Message["LID"])

	for index, value := range event.Command.Message {
		log.Noteln("SET " + index + " " + value)
		gameServer.Set(index, value)
	}
}

// CONN - SHARED (???) called on connection
func (tM *TheaterManager) CONN(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	answerPacket["TIME"] = strconv.FormatInt(time.Now().UTC().Unix(), 10)
	answerPacket["activityTimeoutSecs"] = "30"
	answerPacket["PROT"] = event.Command.Message["PROT"]
	event.Client.WriteFESL(event.Command.Query, answerPacket, 0x0)
	tM.logAnswer(event.Command.Query, answerPacket, 0x0)
}

// EGRS - SERVER sent up, tell us if client is 'allowed' to join
func (tM *TheaterManager) EGRS(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		return
	}

	log.Noteln("wpwww")

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	event.Client.WriteFESL("EGRS", answerPacket, 0x0)
}

// PENT - SERVER sent up when a player joins (entitle player?)
func (tM *TheaterManager) PENT(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		return
	}

	log.Noteln("==============")
	log.Noteln("== got pent ==")
	log.Noteln("==============")

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	answerPacket["PID"] = event.Command.Message["PID"]
	event.Client.WriteFESL("PENT", answerPacket, 0x0)
}

// UPLA - SERVER presumably "update player"? valid response reqiured
func (tM *TheaterManager) UPLA(event gs.EventClientFESLCommand) {
	if !event.Client.IsActive {
		return
	}

	log.Noteln("==============")
	log.Noteln("== got uPLA ==")
	log.Noteln("==============")

	answerPacket := make(map[string]string)
	answerPacket["TID"] = event.Command.Message["TID"]
	answerPacket["PID"] = event.Command.Message["PID"]
	answerPacket["P-cid"] = event.Command.Message["P-cid"]
	log.Noteln(answerPacket)
	event.Client.WriteFESL("UPLA", answerPacket, 0x0)
}

func (tM *TheaterManager) newClient(event gs.EventNewClient) {
	if !event.Client.IsActive {
		log.Noteln("Client left")
		return
	}
	log.Noteln("Client connecting")

	// Start Heartbeat
	event.Client.State.HeartTicker = time.NewTicker(time.Second * 10)
	go func() {
		for {
			if !event.Client.IsActive {
				return
			}
			select {
			case <-event.Client.State.HeartTicker.C:
				if !event.Client.IsActive {
					return
				}
				pingPacket := make(map[string]string)
				pingPacket["TID"] = "0"
				event.Client.WriteFESL("PING", pingPacket, 0x0)
			}
		}
	}()
	event.Client.State.JoinTicker = time.NewTicker(time.Second * 1)
	go func() {
		for {
			if !event.Client.IsActive {
				return
			}
			select {
			case <-event.Client.State.JoinTicker.C:
				if !event.Client.IsActive {
					return
				}
				if !event.Client.State.IsServer {
					if canJoin {
						canJoin = false
						log.Noteln("SENDING EGEG TO GAME CLIENT :D " + localPort)
						ap := make(map[string]string)
						ap["PL"] = "pc"
						ap["TICKET"] = "2018751182"
						ap["PID"] = pid
						ap["I"] = "45.77.79.240"
						ap["P"] = "18569"
						ap["HUID"] = "1"
						ap["EKEY"] = "O65zZ2D2A58mNrZw1hmuJw%3d%3d"
						ap["INT-IP"] = "45.77.79.240"
						ap["INT-PORT"] = "18569"
						ap["SECRET"] = "2587913"
						ap["UGID"] = "7eb6155c-ac70-4567-9fc4-732d56a9334a"
						ap["LID"] = "1"
						ap["GID"] = "5459"
						event.Client.WriteFESL("EGEG", ap, 0x0)

						tM.logAnswer("EGEG", ap, 0x0)
						log.Noteln(ap)

					}
				} else {

					if wantsToJoin {
						wantsToJoin = false
						log.Noteln("SENDING EGRQ TO GAMESERVER FOR PORT " + localPort)
						answerPacket2 := make(map[string]string)
						answerPacket2["TID"] = "6"

						answerPacket2["NAME"] = nickname
						answerPacket2["UID"] = userId
						answerPacket2["PID"] = pid
						answerPacket2["TICKET"] = "2018751182"

						answerPacket2["IP"] = remoteIP
						answerPacket2["PORT"] = remotePort

						answerPacket2["INT-IP"] = localIP
						answerPacket2["INT-PORT"] = localPort

						answerPacket2["PTYPE"] = "P"

						answerPacket2["R-cid"] = userId

						answerPacket2["cid"] = userId

						answerPacket2["R-USER"] = nickname
						answerPacket2["R-UID"] = userId
						answerPacket2["XUID"] = userId
						answerPacket2["R-XUID"] = userId

						answerPacket2["R-U-accid"] = userId
						answerPacket2["R-U-elo"] = "1"
						answerPacket2["R-U-team"] = "1"
						answerPacket2["R-U-kit"] = "2"
						answerPacket2["R-U-lvl"] = "1"
						answerPacket2["R-U-dataCenter"] = "iad"
						answerPacket2["R-U-externalIp"] = remoteIP
						answerPacket2["R-U-internalIp"] = remotePort

						answerPacket2["R-U-category"] = "5"
						answerPacket2["R-U-cid"] = userId

						answerPacket2["R-INT-PORT"] = localPort
						answerPacket2["R-INT-IP"] = localIP

						answerPacket2["LID"] = "1"
						answerPacket2["GID"] = "5459"
						event.Client.WriteFESL("EGRQ", answerPacket2, 0x0)
						tM.logAnswer("EGRQ", answerPacket2, 0x0)
						log.Noteln(answerPacket2)

						canJoin = true
					}

					if wantsToLeaveQueue {
						wantsToLeaveQueue = false
						log.Noteln("SENDING QLVT TO SERVER FOR PORT " + localPort)

						ap := make(map[string]string)
						ap["PID"] = pid
						ap["LID"] = "1"
						ap["GID"] = "5459"
						//event.Client.WriteFESL("QLVT", ap, 0x0)
						tM.logAnswer("QLVT", ap, 0x0)
					}

				}
			}
		}
	}()
}

func (tM *TheaterManager) close(event gs.EventClientTLSClose) {
	log.Noteln("Client closed.")

	if !event.Client.State.HasLogin {
		return
	}

}

func (tM *TheaterManager) error(event gs.EventClientTLSError) {
	log.Noteln("Client threw an error: ", event.Error)
}
