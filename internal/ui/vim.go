package ui

// vimG is a tiny state machine for the vim-style gg multi-key chord.
//
// Behavior:
//   - First g press: arms the chord (returns gWait). The dispatcher should
//     consume the keypress without taking any other action.
//   - Second g press (chord armed): triggers gTop. The dispatcher should
//     perform a "go to top" action whose meaning depends on context
//     (cursor-to-top of list in browse mode, viewport-to-top in zoom mode).
//   - Any other key: clears the armed flag and returns gContinue, signaling
//     that the dispatcher should proceed with normal handling of the key.
//
// G (single press, capital) is intentionally NOT modeled here — it's a
// one-shot binding handled in the regular dispatcher.
type vimG struct {
	armed bool
}

// gResult is the action signal the dispatcher should act on.
type gResult int

const (
	gContinue gResult = iota // not part of a g-chord; dispatch the key normally
	gWait                    // first g of a chord; consume the keypress
	gTop                     // chord completed; trigger goto-top
)

func (v *vimG) step(k string) gResult {
	if k == "g" {
		if v.armed {
			v.armed = false
			return gTop
		}
		v.armed = true
		return gWait
	}
	v.armed = false
	return gContinue
}
