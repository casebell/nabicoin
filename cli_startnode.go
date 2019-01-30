package main

import (
	"fmt"
	"log"
)

func (cli *CLI) startNode(minerAddress string) {
	fmt.Printf("Starting node : ")
	if len(minerAddress) > 0 {
		if ValidateAddress(minerAddress) {
			fmt.Println("Mining is on. Address to receive rewards: ", minerAddress)
		} else {
			log.Panic("Wrong miner address!")
		}
	}
	StartServer(minerAddress)
}
