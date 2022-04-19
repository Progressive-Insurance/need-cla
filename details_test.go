package needcla_test

import (
	"math/rand"
	"testing"
	"time"

	needcla "github.com/progressive-insurance/need-cla"
)

func TestDetailsRequired(t *testing.T) {
	t.Run("NotRequired", func(t *testing.T) {
		d := needcla.Details{}
		if d.Required() {
			t.Errorf("got required from zero value")
		}
	})

	t.Run("Required", func(t *testing.T) {
		rand.Seed(time.Now().Unix())
		for runs := 0; runs < 20; runs++ {

			tt := []bool{false, false, false, false, false, false}
			trues := rand.Intn(5) + 1
			for i := 0; i < trues; i++ {
				tt[i] = true
			}
			rand.Shuffle(len(tt), func(i, j int) {
				tt[i], tt[j] = tt[j], tt[i]
			})

			d := needcla.Details{
				Known:          tt[0],
				Tag:            tt[1],
				BotFile:        tt[2],
				InContributing: tt[3],
				InREADME:       tt[4],
				Action:         tt[5],
			}

			if !d.Required() {
				t.Errorf("got not required from %+v", d)
			}
		}
	})
}
