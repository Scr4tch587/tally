package main

import (
	"fmt"
	"errors"
)

type User struct {
    Roles []string
}	

func (u *User) AddRole(role string) {
    u.Roles = append(u.Roles, role)
}

func IsPositive(n int) bool {
    return n > 0
}

func isEven(n int) bool {
	return (n % 2 == 0)
}

func SafeDivide(a int, b int) (int, error) {
	if b == 0 {
		return 0, errors.New("division by zero")
	}
	return a/b, nil
}

type Account struct {
	id int
	balance float64
}

func NewAccount (id int, initialBalance float64) *Account {
	if initialBalance < 0 {
		initialBalance = 0
	}
	return &Account{id: id, balance: initialBalance}
}

func (a *Account) Deposit(amount float64)  error {
	if amount <= 0 {
		return errors.New("deposit amountmust be positive")
	}
	a.balance += amount
	return nil
}

func (a *Account) Withdraw(amount float64)  error {
	if amount <= 0 {
		return errors.New("withdraw amount must be positive")
	} 
	if amount > a.balance {
		return errors.New("insufficient funds")
	} 
	a.balance -= amount
	return nil
}

func (a *Account) Balance() float64 {
	return a.balance
}

func main() {
	accnt := NewAccount(1, 100.0)
	err := accnt.Deposit(50.0)
	if err != nil {
		fmt.Println("Error:", err)
	}

	err = accnt.Withdraw(30.0)
	if err != nil {
		fmt.Println("Error:", err)
	}

	err = accnt.Withdraw(1000.0)
	if err != nil {
		fmt.Println("Error:", err)
	}

	fmt.Println("Final Balance:", accnt.Balance())
}