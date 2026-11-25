package main

import "example.com/smokerepo/widget"

func main() {
	w := widget.NewWidget("demo")
	_ = widget.SQLStore{}.Save(w)
}
