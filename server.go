package main

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"github.com/sparrc/go-ping"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const protocol = "tcp"
const nodeVersion = 1
const commandLength = 12
const port = "xxxx"
const mainServer = "xxx.xxx.xxx.xxx:" + port
const key = "xxxxxxxxxxxxxxxx"

var flagMining = false
var remoteAddress string
var nodeAddress string
var miningAddress string
var startMiningTime time.Time
var startCleanTime time.Time
var startAddingBlockTime time.Time
var blocksInTransit = [][]byte{}
var knownNodes = []string{mainServer}
var nodes = make(map[string]string)
var mempool = make(map[string]Transaction)
var storemempool = make(map[string]Transaction)
var txIDPool = make(map[string]string)
var mutex = new(sync.Mutex)
var wg sync.WaitGroup
var cryptoBlock, _ = aes.NewCipher([]byte(key))

type addr struct {
	AddrList []string
}

type block struct {
	AddrFrom string
	Block    []byte
}

type getdata struct {
	AddrFrom string
	Type     string
	ID       []byte //none use
	Height   int
}

type inv struct {
	AddrFrom string
	Type     string
	Items    [][]byte
}

type tx struct {
	AddrFrom    string
	Transaction []byte
}

type bal struct {
	AddrFrom   string
	RemoteAddr string
	PublicKey  string
	Balance    int
}

type remote struct {
	AddrFrom string
	From     string
	To       string
	Amount   int
}

type verzion struct {
	Version    int
	BestHeight int
	AddrFrom   string
}

type nodePing struct {
	node     string
	pingTime time.Duration
}

type nodePings []nodePing

type ByPingTime struct {
	nodePings
}

func commandToBytes(command string) []byte {
	var bytes [commandLength]byte

	for i, c := range command {
		bytes[i] = byte(c)
	}

	return bytes[:]
}

func bytesToCommand(bytes []byte) string {
	var command []byte

	for _, b := range bytes {
		if b != 0x0 {
			command = append(command, b)
		}
	}

	return fmt.Sprintf("%s", command)
}

func extractCommand(request []byte) []byte {
	return request[:commandLength]
}

func sendAddr(address string) {
	nodes := addr{knownNodes}
	//nodes.AddrList = append(nodes.AddrList, nodeAddress)
	payload := gobEncode(nodes)
	request := append(commandToBytes("addr"), payload...)

	sendData(address, request)
}

func sendBlock(addr string, b *Block) bool {
	data := block{nodeAddress, b.Serialize()}
	payload := gobEncode(data)
	request := append(commandToBytes("block"), payload...)

	return sendData(addr, request)
}

func sendData(addr string, data []byte) bool {
	var timeout time.Duration

	//fmt.Printf("[send data] try send to %s\n", addr)

	timeout = 1 * time.Second
	dial := net.Dialer{Timeout: timeout}

	for i := 0; i < 4; i++ {
		conn, err := dial.Dial(protocol, addr)
		if err != nil {
			continue
		} else {
			defer conn.Close()

			_, err = io.Copy(conn, bytes.NewReader(data))
			if err != nil {
				log.Panic(err)
			} else {
				//fmt.Printf("[send data] sending success!!!")
			}

			break
		}

		return false
	}

	return true
}

func sendInv(address, kind string, items [][]byte) bool {
	inventory := inv{nodeAddress, kind, items}
	payload := gobEncode(inventory)
	request := append(commandToBytes("inv"), payload...)

	return sendData(address, request)
}

func sendGetData(address, kind string, id []byte, height int) bool {
	payload := gobEncode(getdata{nodeAddress, kind, id, height})
	request := append(commandToBytes("getdata"), payload...)

	return sendData(address, request)
}

func sendTx(addr string, tnx *Transaction) bool {
	data := tx{nodeAddress, tnx.Serialize()}
	payload := gobEncode(data)
	request := append(commandToBytes("tx"), payload...)

	return sendData(addr, request)
}

func sendVersion(addr string, bc *Blockchain) bool {
	bestHeight := bc.GetBestHeight()
	payload := gobEncode(verzion{nodeVersion, bestHeight, nodeAddress})

	request := append(commandToBytes("version"), payload...)

	return sendData(addr, request)
}

func handleAddr(request []byte, bc *Blockchain) {
	var buff bytes.Buffer
	var payload addr

	buff.Write(request[commandLength:])
	dec := gob.NewDecoder(&buff)
	err := dec.Decode(&payload)
	if err != nil {
		log.Println(err)

		return
	}

	addrList := make(map[string]int)

	for _, address := range payload.AddrList {
		if address != "" {
			addrList[address] = 0
		}
	}

	for _, node_addr := range knownNodes {
		if node_addr != "" {
			addrList[node_addr] = 0
		}
	}

	if len(addrList) > 0 {
		knownNodes = nil
	}

	for key, _ := range addrList {
		knownNodes = append(knownNodes, key)
	}

	writeAddressToFile()
	//fmt.Printf("[handle addr] There are %d known nodes now!\n", len(knownNodes))
}

func handleBlock(request []byte, bc *Blockchain) {
	var buff bytes.Buffer
	var payload block

	buff.Write(request[commandLength:])
	dec := gob.NewDecoder(&buff)
	err := dec.Decode(&payload)
	if err != nil {
		log.Panic(err)
	}

	payload.AddrFrom = remoteAddress

	blockData := payload.Block
	block := DeserializeBlock(blockData)

	localBestHeight := bc.GetBestHeight()

	if (block.Height == (localBestHeight + 1)) || (block.Height == localBestHeight) {
		//fmt.Printf("[handle block] local Highest Height:%d\n", localBestHeight)
		//fmt.Printf("[handle block] Recevied a new block!(height:%d)\n", block.Height)

		bc.AddBlock(payload.AddrFrom, block)

		UTXOSet := UTXOSet{bc}
		UTXOSet.Reindex()
	}
}

func handleInv(request []byte, bc *Blockchain) {
	var buff bytes.Buffer
	var payload inv

	buff.Write(request[commandLength:])
	dec := gob.NewDecoder(&buff)
	err := dec.Decode(&payload)
	if err != nil {
		log.Panic(err)
	}

	payload.AddrFrom = remoteAddress

	//fmt.Printf("[handle inv] Recevied inventory with %d %s\n", len(payload.Items), payload.Type)

	if payload.Type == "block" {
		blocksInTransit = payload.Items

		//sendGetData
		blockHash := payload.Items[0]
		block, err := bc.GetBlock(blockHash)
		if err != nil {
			fmt.Println(err)
		}

		sendGetData(payload.AddrFrom, "block", nil, block.Height)

		newInTransit := [][]byte{}
		for _, b := range blocksInTransit {
			if bytes.Compare(b, blockHash) != 0 {
				newInTransit = append(newInTransit, b)
			}
		}
		blocksInTransit = newInTransit
	}

	if payload.Type == "tx" {
		txID := payload.Items[0]

		if mempool[hex.EncodeToString(txID)].ID == nil {
			sendGetData(payload.AddrFrom, "tx", txID, 0)
		}
	}
}

func handleGetData(request []byte, bc *Blockchain) {
	var buff bytes.Buffer
	var payload getdata

	buff.Write(request[commandLength:])
	dec := gob.NewDecoder(&buff)
	err := dec.Decode(&payload)
	if err != nil {
		log.Panic(err)
	}

	payload.AddrFrom = remoteAddress

	if payload.Type == "block" {
		height := payload.Height
		if height == 0 {
			return
		}

		//fmt.Printf("[handleGetData] GetBlockByHeight:%d\n", height)
		block, isHave := bc.GetBlockByHeight(height)

		if isHave && (block.Height > 0) {
			for i := 0; i < 4; i++ {
				if sendBlock(payload.AddrFrom, &block) {
					break
				}
			}
		}
	}

	if payload.Type == "tx" {
		txID := hex.EncodeToString(payload.ID)
		tx := storemempool[txID]

		sendTx(payload.AddrFrom, &tx)
	}

	if payload.Type == "addr" {
		sendAddr(payload.AddrFrom)
	}
}

func handleTx(request []byte, bc *Blockchain) {
	//fmt.Println("[handle tx] handleTx")
	var buff bytes.Buffer
	var payload tx

	buff.Write(request[commandLength:])
	dec := gob.NewDecoder(&buff)
	err := dec.Decode(&payload)
	if err != nil {
		log.Panic(err)
	}

	//fmt.Printf("[handle Tx] remoteAddress = %s\n", remoteAddress)
	payload.AddrFrom = remoteAddress

	txData := payload.Transaction
	tx := DeserializeTransaction(txData)

	if txIDPool[hex.EncodeToString(tx.ID)] == hex.EncodeToString(tx.ID) {
		//fmt.Println("[handle tx] not new tx")

		currentTime := time.Now()
		elapsedCleanTime := currentTime.Sub(startCleanTime)
		if elapsedCleanTime.Seconds() < (60 * 10) {
			return
		}

		for key, _ := range txIDPool {
			delete(txIDPool, key)
		}
	}

	if bc.VerifyTransaction(&tx) == false {
		//fmt.Println("[handle tx] tx is not ok")

		sendVersion(payload.AddrFrom, bc)

		return

	}

	mempool[hex.EncodeToString(tx.ID)] = tx
	storemempool[hex.EncodeToString(tx.ID)] = tx

	txIDPool[hex.EncodeToString(tx.ID)] = hex.EncodeToString(tx.ID)
	startCleanTime = time.Now()

	//fmt.Printf("[handle]add tx.ID to tx id pool(%s)\n", hex.EncodeToString(tx.ID))
	//fmt.Printf("[handle tx] history tx count: %d\n", len(storemempool))

	for _, node := range knownNodes {
		if node != nodeAddress && node != payload.AddrFrom {
			//fmt.Printf("[handle tx] send tx to %s", node)
			sendData(node, request)
		}
	}

	fmt.Printf("[handle tx] mempool size = %d(%s)\n", len(mempool), miningAddress)

	if (len(mempool) >= 20) && (len(miningAddress) > 0) {
		startMiningTime = time.Now()

		var txs []*Transaction
		txs = nil

		for key, tx := range mempool {
			if bc.VerifyTransaction(&tx) {
				txs = append(txs, &tx)
			} else {
				fmt.Println("[handle tx] Not Verify Trasaction!!!")
			}

			delete(mempool, key)
		}

		if len(txs) == 0 {
			fmt.Println("[handle tx] All transactions are invalid! Waiting for new ones...")

			return
		}

		go func() {
			defer wg.Done()

			cbTx := NewCoinbaseTX(miningAddress, "")
			txs = append(txs, cbTx)

			mutex.Lock()

			startAddingBlockTime = time.Now()
			for {
				forTranslateBlockTime := time.Now()

				elapsedForTranslateBlockTime := forTranslateBlockTime.Sub(startAddingBlockTime)

				if elapsedForTranslateBlockTime.Seconds() > 10 {
					break
				}

				runtime.Gosched()
			}

			log.Println("[handle tx] ========== start minning ==========")

			wg.Add(1)

			time.Sleep(2)

			bc.MineBlock(txs)
			UTXOSet := UTXOSet{bc}
			UTXOSet.Reindex()

			log.Println("[handle tx] =======!!!!New block is mined!!!!=======")

			for _, node := range knownNodes {
				if nodeAddress != node {
					//fmt.Printf("[handle tx] try send version to %s", node)

					sendVersion(node, bc)
				}
			}
			mutex.Unlock()
		}()

		if len(txIDPool) >= 300 {
			fmt.Println("[handle tx] clean tx id pool")

			count := 0

			for key, _ := range txIDPool {
				delete(txIDPool, key)

				if count == 200 {
					break
				}

				count++
			}
		}
	}

}

func emptyMining(bc *Blockchain) {
	var txs []*Transaction

	startMiningTime = time.Now()
	defer wg.Done()

	txs = nil

	for key, tx := range mempool {
		if bc.VerifyTransaction(&tx) {
			txs = append(txs, &tx)
		} else {
			fmt.Println("[handle tx] Not Verify Trasaction!!!")
		}

		delete(mempool, key)
	}

	cbTx := NewCoinbaseTX(miningAddress, "")
	txs = append(txs, cbTx)

	mutex.Lock()

	wg.Add(1)

	startAddingBlockTime = time.Now()
	for {
		forTranslateBlockTime := time.Now()

		elapsedForTranslateBlockTime := forTranslateBlockTime.Sub(startAddingBlockTime)

		if elapsedForTranslateBlockTime.Seconds() > 2 {
			break
		}

		runtime.Gosched()
	}

	log.Println("[handle tx] ========== start minning ==========")

	bc.MineBlock(txs)
	UTXOSet := UTXOSet{bc}
	UTXOSet.Reindex()

	log.Println("[handle tx] =======!!!!New block is mined!!!!=======")

	for _, node := range knownNodes {
		if nodeAddress != node {
			//fmt.Printf("[handle tx] try send version to %s", node)

			sendVersion(node, bc)
		}
	}
	mutex.Unlock()
}

func handleVersion(request []byte, bc *Blockchain) {
	var buff bytes.Buffer
	var payload verzion

	buff.Write(request[commandLength:])
	dec := gob.NewDecoder(&buff)
	err := dec.Decode(&payload)
	if err != nil {
		log.Panic(err)
	}

	payload.AddrFrom = remoteAddress

	myBestHeight := bc.GetBestHeight()
	foreignerBestHeight := payload.BestHeight

	if myBestHeight < foreignerBestHeight {
		sendAddr(payload.AddrFrom)

		//fmt.Printf("[handle version] send get block with local highest height + 1:%d\n", myBestHeight+1)

		sendGetData(payload.AddrFrom, "block", nil, myBestHeight+1)
	} else if myBestHeight > foreignerBestHeight {
		sendVersion(payload.AddrFrom, bc)

		//fmt.Printf("[handle version] send version to %s\n", payload.AddrFrom)
	} else {
		sendAddr(payload.AddrFrom)
		sendGetData(payload.AddrFrom, "addr", nil, 0)

		sendGetData(payload.AddrFrom, "block", nil, myBestHeight)
		sendVersion(payload.AddrFrom, bc)
	}

	if !nodeIsKnown(payload.AddrFrom) {
		sendAddr(payload.AddrFrom)
		knownNodes = append(knownNodes, payload.AddrFrom)
	}
}

func handleConnection(conn net.Conn, bc *Blockchain) {
	defer func() {
		conn.Close()
	}()

	tmpAddr := conn.RemoteAddr().String()
	strTmp := strings.Split(tmpAddr, ":")
	remoteAddress = strTmp[0] + ":" + port

	request, err := ioutil.ReadAll(conn)
	if err != nil {
		log.Panic(err)
	}
	command := bytesToCommand(request[:commandLength])

	switch command {
	case "addr":
		//fmt.Printf("[handle connection] Received %s command\n", command)
		handleAddr(request, bc)
	case "block":
		//fmt.Printf("[handle connection] Received %s command\n", command)
		go func() {
			wg.Wait()

			handleBlock(request, bc)
		}()
	case "inv":
		//fmt.Printf("[handle connection] Received %s command\n", command)
		go handleInv(request, bc)
	case "getdata":
		//fmt.Printf("[handle connection] Received %s command\n", command)
		go handleGetData(request, bc)
	case "tx":
		//fmt.Printf("[handle connection] Received %s command\n", command)
		handleTx(request, bc)
	case "version":
		//fmt.Printf("[handle connection] Received %s command\n", command)
		go handleVersion(request, bc)
	default:
		//fmt.Println("[handle connection] Unknown command!")
	}
}

func getLocalhost() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		os.Stderr.WriteString("Oops: " + err.Error() + "\n")
		os.Exit(1)
	}

	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				localhostAddr := ipnet.IP.String()

				//fmt.Println(localhostAddr + ":" + "60199")

				return localhostAddr
			}
		}
	}

	//fmt.Println("localhost")

	return "localhost"
}

// StartServer starts a node
func StartServer(minerAddress string) {
	localhostAddr := getLocalhost()

	nodeAddress = fmt.Sprintf("%s:%s", localhostAddr, port)
	miningAddress = minerAddress
	ln, err := net.Listen(protocol, nodeAddress)
	if err != nil {
		log.Panic(err)
	}
	defer func() {
		ln.Close()
		createAddressFile()
		writeAddressToFile()

		fmt.Println("EXIT NAVI-COIN SYSTEM")
	}()

	startMiningTime = time.Now()
	startCleanTime = time.Now()
	startAddingBlockTime = time.Now()

	bc := NewBlockchain()

	//read, sort, delete duplication
	readAddressFromFile()

	if len(knownNodes) > 1 {
		testNodeConnectable()

		ping_sort_address()
	}

	for _, node := range knownNodes {
		if nodeAddress != node {
			//fmt.Printf("try send version to %s", node)

			result := sendVersion(node, bc)
			if result == true {
				break
			}
		}
	}

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		ln.Close()
		createAddressFile()
		writeAddressToFile()

		fmt.Println("EXIT NAVI-COIN SYSTEM")
		os.Exit(1)
	}()

	if len(miningAddress) > 0 {
		go func() {
			for {
				currentTime := time.Now()
				elapsedMiningTime := currentTime.Sub(startMiningTime)

				if elapsedMiningTime.Seconds() > (60 * 5) {
					emptyMining(bc)
				}

				runtime.Gosched()
			}
		}()
	}

	for {
		//fmt.Println("[server] wait.....")
		conn, err := ln.Accept()
		if err != nil {
			log.Panic(err)
		}

		//fmt.Println("[server] accepted...")
		handleConnection(conn, bc)
	}
}

func testNodeConnectable() {
	//fmt.Println("[test node] test connect to node and sort node")
	//fmt.Printf("input node address count: %d\n", len(knownNodes))

	if len(knownNodes) > 1 {
		var timeout time.Duration

		knownNodes = nil

		timeout = 1 * time.Second
		dial := net.Dialer{Timeout: timeout}

		for node, _ := range nodes {
			conn, err := dial.Dial(protocol, node)
			if err != nil {
				if node != mainServer {
					delete(nodes, node)

					continue
				}
			} else {
				conn.Close()
			}

			knownNodes = append(knownNodes, node)
		}

		knownNodes = append(knownNodes, mainServer)

		fmt.Printf("%d node addresses are waken\n", len(knownNodes))
	}
}

func createAddressFile() {
	file, err := os.OpenFile(".knownNodes", os.O_CREATE|os.O_TRUNC, os.FileMode(0644))
	if err != nil {
		fmt.Println(err)
	} else {
		file.Close()
	}
}

func readAddressFromFile() {
START_READ_FILE:
	nodeFromFile, err := readLines(".knownNodes")
	if err != nil {
		//fmt.Printf("readLine's err: %s", err)
		createAddressFile()

		goto START_READ_FILE
	}

	knownNodes = nil
	for _, node := range nodeFromFile {
		//fmt.Printf("readed addr : %s\n", node)
		address := make([]byte, 16)

		cryptoNode := []byte(node)
		cryptoNode = cryptoNode[:16]
		cryptoBlock.Decrypt(address, cryptoNode)

		posColon := strings.Index(string(address), ":")
		address = address[:posColon+1]
		strAddress := string(address)
		strAddress += port
		knownNodes = append(knownNodes, strAddress)
		nodes[strAddress] = strAddress
	}

	if len(knownNodes) < 1 {
		knownNodes = append(knownNodes, mainServer)
		nodes[mainServer] = mainServer
	}
}

func writeAddressToFile() {
	write_err := writeLines(knownNodes, ".knownNodes")
	if write_err != nil {
		log.Fatalf("writeLines: %s", write_err)
	}
}

func gobEncode(data interface{}) []byte {
	var buff bytes.Buffer

	enc := gob.NewEncoder(&buff)
	err := enc.Encode(data)
	if err != nil {
		log.Panic(err)
	}

	return buff.Bytes()
}

func nodeIsKnown(addr string) bool {
	if nodes[addr] != "" {
		return true
	} else {
		return false
	}
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// writeLines writes the lines to the given file.
func writeLines(lines []string, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	w := bufio.NewWriter(file)
	for _, line := range lines {
		cryptoNode := make([]byte, len(line))
		cryptoBlock.Encrypt(cryptoNode, []byte(line))
		fmt.Fprintln(w, string(cryptoNode))
	}
	return w.Flush()
}

func ping_sort_address() {
	//fmt.Printf("[ping sort] node address count: %d", len(knownNodes))

	if len(knownNodes) > 1 {
		p := nodePings{}

		for node, _ := range nodes {
			if nodeAddress == node {
				continue
			}

			str_ip4 := strings.Split(node, ":")
			pinger, err := ping.NewPinger(str_ip4[0])
			if err != nil {
				//panic(err)
				continue
			}

			pinger.SetPrivileged(true)

			pinger.Count = 1
			pinger.Timeout = time.Second
			pinger.Run() // block until finished
			pinger.SetPrivileged(false)
			stats := pinger.Statistics() // get send/receive/rtt stats

			var tmpNodePing nodePing

			tmpNodePing.node = node

			tmpNodePing.pingTime = stats.AvgRtt
			if tmpNodePing.pingTime == 0 {
				tmpNodePing.pingTime = 100
			}

			//fmt.Printf("[ping sort] ping %s : %v", str_ip4[0], stats.AvgRtt)

			p = append(p, tmpNodePing)

		}

		//fmt.Println("[ping sort] sort by ping")

		sort.Sort(ByPingTime{p})

		knownNodes = nil
		for _, node := range p {
			knownNodes = append(knownNodes, node.node)
		}
	}
}

func (nps nodePings) Len() int {
	return len(nps)
}

func (p ByPingTime) Less(i, j int) bool {
	return p.nodePings[i].pingTime < p.nodePings[j].pingTime
}

func (p ByPingTime) Swap(i, j int) {
	p.nodePings[i], p.nodePings[j] = p.nodePings[j], p.nodePings[i]
}
