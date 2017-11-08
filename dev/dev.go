package dev

import (
	"encoding/json"
	"fmt"
)

func JSON(data interface{}) {
	j, err := json.MarshalIndent(data, "", "  ")

	if err != nil {
		panic("Could not marshal data: " + err.Error())
	}

	fmt.Println(string(j))
}

func Println(args ...interface{}) {
	fmt.Println(args...)
}
