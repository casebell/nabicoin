package main

import (
	"fmt"
	"log"
)

func (cli *CLI) send(from, to string, amount int) {
	if !ValidateAddress(from) {
		log.Panic("ERROR: Sender address is not valid")
	}
	if !ValidateAddress(to) {
		log.Panic("ERROR: Recipient address is not valid")
	}

	bc := NewBlockchain()
	UTXOSet := UTXOSet{bc}
	defer bc.db.Close()

	wallets, err := NewWallets()
	if err != nil {
		log.Panic(err)
	}

	localhostAddr := getLocalhost()

	nodeAddress = fmt.Sprintf("%s:%s", localhostAddr, port)

	readAddressFromFile()

	if len(knownNodes) > 1 {
		testNodeConnectable()

		ping_sort_address()
	}

	wallet := wallets.GetWallet(from)

	tx := NewUTXOTransaction(&wallet, to, amount, &UTXOSet)

	if bc.VerifyTransaction(tx) == false {
		fmt.Println("#maked tx is fault.")

		return
	} else {
		fmt.Println("#maked tx is ok.")
	}

	for idx := 0; idx < len(knownNodes); idx++ {
		if knownNodes[idx] == nodeAddress {
			continue
		}

		result := sendTx(knownNodes[idx], tx)
		if result {
			txs := []*Transaction{tx}

			newBlock, err := bc.MineBlock(txs)
			if err != nil {
				log.Println(err)
				return
			}

			UTXOSet.Update(newBlock)

			fmt.Println("Success!")

			return
		}
	}

	fmt.Println("fail!")
}
