package main

import (
	"fmt"
	"log"
	"github.com/leekchan/accounting"
)

func (cli *CLI) getBalance(address string) int {
	if !ValidateAddress(address) {
		log.Panic("ERROR: Address is not valid")
	}

	bc := NewBlockchain()
	UTXOSet := UTXOSet{bc}
	defer bc.db.Close()

	balance := 0
	pubKeyHash := Base58Decode([]byte(address))
	pubKeyHash = pubKeyHash[1 : len(pubKeyHash)-4]

	UTXOs := UTXOSet.FindUTXO(pubKeyHash)

	for _, out := range UTXOs {
		balance += out.Value
	}

	ac := accounting.Accounting{Symbol: "", Precision: 0}
	fmt.Printf("Balance of '%s': %s\n", address, ac.FormatMoney(balance))

	return balance
}
