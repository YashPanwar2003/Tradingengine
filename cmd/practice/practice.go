package main

import (
	"fmt"
)

func sendData(channel chan int, x int){
	channel<-x
}



func main(){
	sampleChannel:=make(chan int,10)
	for i:= range 100{
		go sendData(sampleChannel,i)
	}
	for {
		item,ok:=<-sampleChannel
		if !ok {
			break
		}
		fmt.Println(item)
	}

}