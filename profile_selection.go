package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kevmo314/wechat-cli/internal/native"
)

type profileSwitchRequest struct{ Profile string }

func (e *profileSwitchRequest) Error() string { return "switch profile to " + e.Profile }
func profileStore(base Store, configRoot, requested string, explicitDir bool) (Store, error) {
	profile, err := native.NormalizeProfile(requested)
	if err != nil {
		return Store{}, err
	}
	slug := profile
	if profile == native.AndroidTabletProfile {
		slug = "tablet"
	}
	if explicitDir {
		state, err := (nativeStore{filepath.Join(base.Dir, "native")}).state()
		if err != nil {
			return Store{}, err
		}
		if state.Account != nil {
			saved, _ := native.NormalizeProfile(state.Account.Profile)
			if saved == profile {
				return base, nil
			}
		}
		child := Store{filepath.Join(base.Dir, "profiles", slug)}
		childState, err := (nativeStore{filepath.Join(child.Dir, "native")}).state()
		if err != nil {
			return Store{}, err
		}
		if childState.Account != nil {
			saved, _ := native.NormalizeProfile(childState.Account.Profile)
			if saved != profile {
				return Store{}, fmt.Errorf("profile directory contains a different account type")
			}
			return child, nil
		}
		if state.Account == nil {
			return base, nil
		}
		return child, nil
	}
	target := Store{filepath.Join(base.Dir, "profiles", slug)}
	candidates := []Store{target, base}
	if profile == native.AndroidTabletProfile {
		candidates = append(candidates, Store{filepath.Join(configRoot, "wechat-tablet")})
	}
	for _, candidate := range candidates {
		state, err := (nativeStore{filepath.Join(candidate.Dir, "native")}).state()
		if err != nil {
			if candidate.Dir == target.Dir {
				return Store{}, err
			}
			continue
		}
		if state.Account != nil {
			saved, _ := native.NormalizeProfile(state.Account.Profile)
			if saved == profile {
				return candidate, nil
			}
		}
	}
	return target, nil
}
func loginProfileArgument(args []string) (string, bool, error) {
	for i, arg := range args {
		arg = strings.TrimLeft(arg, "-")
		if value, ok := strings.CutPrefix(arg, "profile="); ok {
			p, err := native.NormalizeProfile(value)
			return p, true, err
		}
		if arg == "profile" {
			if i+1 == len(args) {
				return "", true, fmt.Errorf("--profile needs a value")
			}
			p, err := native.NormalizeProfile(args[i+1])
			return p, true, err
		}
	}
	return "", false, nil
}

type profilePreference struct {
	Profile string `json:"profile"`
}

func preferredProfileStore(base Store, configRoot string, custom ...bool) Store {
	explicit := len(custom) > 0 && custom[0]
	var preference profilePreference
	if err := (nativeStore{base.Dir}).load("preferences.json", &preference); err == nil && preference.Profile != "" {
		if selected, err := profileStore(base, configRoot, preference.Profile, explicit); err == nil {
			state, err := (nativeStore{filepath.Join(selected.Dir, "native")}).state()
			if err == nil && state.Account != nil {
				return selected
			}
		}
	}
	if explicit {
		return base
	}
	if tablet, err := profileStore(base, configRoot, native.AndroidTabletProfile, false); err == nil {
		state, err := (nativeStore{filepath.Join(tablet.Dir, "native")}).state()
		if err == nil && state.Account != nil {
			return tablet
		}
	}
	return base
}
func rememberProfile(base Store, profile string) error {
	if err := base.init(); err != nil {
		return err
	}
	return (nativeStore{base.Dir}).save("preferences.json", profilePreference{Profile: profile})
}
