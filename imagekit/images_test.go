package imagekit

import (
	"io/ioutil"
	"testing"
)

func TestAvatar(t *testing.T) {
	bytes, err := ioutil.ReadFile("testdata/avatar.jpg")
	if err != nil {
		t.Error(err)
		return
	}

	finalBytes, _, err := GetThumbnail(bytes, 300, 300, 1024*20)
	if err != nil {
		t.Error(err)
		return
	}

	ioutil.WriteFile("testdata/avatar.done.jpg", finalBytes, 0644)
}
