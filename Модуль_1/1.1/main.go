package main

import (
	"fmt"
	"time"
)

type Skill int

const (
	GO      Skill = 1
	DOT_NET Skill = 2
	JAVA    Skill = 3
	PYTHON  Skill = 10
)

type Human struct {
	Name string
	Age  int
}

func (h *Human) Hello() {
	fmt.Printf("%s говорит привет.\n", h.Name)
}

func (h *Human) GetBirthYear() int {
	currentYear := time.Now().Year()
	return currentYear - h.Age
}

type Action struct {
	Human
	Skill Skill
}

func main() {
	act := Action{
		Human: Human{Name: "Алексей", Age: 25},
		Skill: Skill(GO),
	}

	birthYear := act.GetBirthYear()
	fmt.Printf("%s родился примерно в %d году.\n", act.Name, birthYear)
	act.Hello()
}
