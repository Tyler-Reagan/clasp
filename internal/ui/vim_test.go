package ui

import "testing"

func TestVimG_SingleGArmsCombo(t *testing.T) {
	v := vimG{}
	if got := v.step("g"); got != gWait {
		t.Errorf("first g: got %v, want gWait", got)
	}
	if !v.armed {
		t.Error("expected armed=true after first g")
	}
}

func TestVimG_SecondGTriggersTop(t *testing.T) {
	v := vimG{}
	v.step("g") // arm
	got := v.step("g")
	if got != gTop {
		t.Errorf("second g: got %v, want gTop", got)
	}
	if v.armed {
		t.Error("armed should clear after triggering top")
	}
}

func TestVimG_AnyOtherKeyCancelsAndContinues(t *testing.T) {
	for _, k := range []string{"j", "k", "G", "q", "ctrl+u", "esc", " ", "?"} {
		t.Run(k, func(t *testing.T) {
			v := vimG{armed: true}
			got := v.step(k)
			if got != gContinue {
				t.Errorf("step(%q): got %v, want gContinue", k, got)
			}
			if v.armed {
				t.Errorf("step(%q) did not clear armed flag", k)
			}
		})
	}
}

func TestVimG_GAloneFromUnarmedReturnsContinue(t *testing.T) {
	// A G with no prior g should be gContinue (let the End binding handle it),
	// not gTop. The chord requires gg specifically.
	v := vimG{}
	got := v.step("G")
	if got != gContinue {
		t.Errorf("G with no prior g: got %v, want gContinue", got)
	}
	if v.armed {
		t.Error("G should not arm the chord")
	}
}

func TestVimG_NonGAfterNonGStaysUnarmedAndContinues(t *testing.T) {
	v := vimG{}
	if got := v.step("j"); got != gContinue {
		t.Errorf("step(j): got %v, want gContinue", got)
	}
	if v.armed {
		t.Error("j should not arm")
	}
}

func TestVimG_ThreeGsTriggersOnceThenWaits(t *testing.T) {
	// ggg → first triggers gTop, third arms again (gWait).
	v := vimG{}
	if got := v.step("g"); got != gWait {
		t.Fatalf("1st g: got %v, want gWait", got)
	}
	if got := v.step("g"); got != gTop {
		t.Fatalf("2nd g: got %v, want gTop", got)
	}
	if got := v.step("g"); got != gWait {
		t.Fatalf("3rd g: got %v, want gWait", got)
	}
}

func TestVimG_GFollowedByJCancelsThenJDispatchesNormally(t *testing.T) {
	// g (arm), j (cancel chord, continue) — the j press should be reported as
	// gContinue so the caller dispatches it normally.
	v := vimG{}
	v.step("g")
	got := v.step("j")
	if got != gContinue {
		t.Errorf("got %v, want gContinue", got)
	}
	if v.armed {
		t.Error("j after g should clear armed")
	}
}
