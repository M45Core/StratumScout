package probe

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
)

var agents = []string{"cgminer/4.11.1", "bfgminer/5.5.0", "bmminer/2.0", "braiins-os/24.04", "whatsminer/1.0"}
var workers = []string{"worker", "miner", "rig", "asic", "home", "garage", "s19", "m30s"}

type Identity struct {
	Username     string
	Agent        string
	PayoutScript []byte
}

func RandomIdentity() (Identity, error) {
	payload := make([]byte, 21)
	payload[0] = 0
	if _, err := rand.Read(payload[1:]); err != nil {
		return Identity{}, err
	}
	first := sha256.Sum256(payload)
	second := sha256.Sum256(first[:])
	address := base58(append(payload, second[:4]...))
	a, err := randomChoice(agents)
	if err != nil {
		return Identity{}, err
	}
	w, err := randomChoice(workers)
	if err != nil {
		return Identity{}, err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(100))
	if err != nil {
		return Identity{}, err
	}
	payoutScript := append([]byte{0x76, 0xa9, 0x14}, payload[1:]...)
	payoutScript = append(payoutScript, 0x88, 0xac)
	return Identity{Username: fmt.Sprintf("%s.%s%d", address, w, n.Int64()), Agent: a, PayoutScript: payoutScript}, nil
}

func randomChoice(items []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(items))))
	if err != nil {
		return "", err
	}
	return items[n.Int64()], nil
}

func base58(input []byte) string {
	alphabet := "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	x := new(big.Int).SetBytes(input)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)
	out := make([]byte, 0, 35)
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		out = append(out, alphabet[mod.Int64()])
	}
	for _, b := range input {
		if b != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
