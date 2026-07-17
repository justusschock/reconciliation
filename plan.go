package reconciliation

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrInvalidPlan is wrapped by every plan validation failure.
	ErrInvalidPlan = errors.New("reconciliation: invalid plan")
	// ErrInvalidActionID identifies an empty action or dependency ID.
	ErrInvalidActionID = errors.New("reconciliation: invalid action ID")
	// ErrDuplicateAction identifies two actions with the same ID.
	ErrDuplicateAction = errors.New("reconciliation: duplicate action")
	// ErrMissingDependency identifies an edge to an absent action.
	ErrMissingDependency = errors.New("reconciliation: missing dependency")
	// ErrDuplicateDependency identifies a repeated edge from one action.
	ErrDuplicateDependency = errors.New("reconciliation: duplicate dependency")
	// ErrPlanCycle identifies a dependency cycle.
	ErrPlanCycle = errors.New("reconciliation: plan cycle")
)

// ActionID identifies one action within a plan.
type ActionID string

// Action is one planned mutation. DependsOn identifies runtime prerequisites,
// not only apply-order hints: rollback retains the transitive dependencies of
// any completed action that cannot be undone. Value ownership is shallow.
type Action[A any] struct {
	ID         ActionID
	DependsOn  []ActionID
	Value      A
	Reversible bool
}

// Undo contains the adapter-defined value needed to reverse an action.
// Value ownership is shallow.
type Undo[U any] struct {
	ActionID ActionID
	Value    U
}

// Plan is a directed acyclic graph of actions. Action values are shallowly
// owned; functions in this package copy action and dependency slice storage.
type Plan[A any] struct {
	Actions []Action[A]
}

// ValidatePlan verifies that all action identifiers and dependencies describe
// a directed acyclic graph.
func ValidatePlan[A any](plan Plan[A]) error {
	_, err := validateAndCopyPlan(plan)
	return err
}

// Ordered returns a deterministic topological order. Among actions whose
// dependencies are satisfied, the lexicographically smallest ID is selected.
func Ordered[A any](plan Plan[A]) ([]Action[A], error) {
	canonical, err := validateAndCopyPlan(plan)
	if err != nil {
		return nil, err
	}

	actions := make(map[ActionID]Action[A], len(canonical.Actions))
	dependents := make(map[ActionID][]ActionID, len(canonical.Actions))
	remaining := make(map[ActionID]int, len(canonical.Actions))
	ready := make([]ActionID, 0, len(canonical.Actions))
	for _, action := range canonical.Actions {
		actions[action.ID] = action
		remaining[action.ID] = len(action.DependsOn)
		if len(action.DependsOn) == 0 {
			ready = append(ready, action.ID)
		}
		for _, dependency := range action.DependsOn {
			dependents[dependency] = append(dependents[dependency], action.ID)
		}
	}
	slices.Sort(ready)
	for dependency := range dependents {
		slices.Sort(dependents[dependency])
	}

	ordered := make([]Action[A], 0, len(canonical.Actions))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		ordered = append(ordered, actions[id])
		for _, dependent := range dependents[id] {
			remaining[dependent]--
			if remaining[dependent] == 0 {
				ready = insertActionID(ready, dependent)
			}
		}
	}
	return ordered, nil
}

func validateAndCopyPlan[A any](plan Plan[A]) (Plan[A], error) {
	canonical := Plan[A]{Actions: make([]Action[A], len(plan.Actions))}
	byID := make(map[ActionID]int, len(plan.Actions))
	for index, action := range plan.Actions {
		action.DependsOn = slices.Clone(action.DependsOn)
		slices.Sort(action.DependsOn)
		canonical.Actions[index] = action
		if action.ID == "" {
			return Plan[A]{}, invalidPlan(ErrInvalidActionID, "action %d has an empty ID", index)
		}
		if first, exists := byID[action.ID]; exists {
			return Plan[A]{}, invalidPlan(ErrDuplicateAction, "action %q duplicates action %d", action.ID, first)
		}
		byID[action.ID] = index
	}

	orderedIDs := make([]ActionID, 0, len(byID))
	for id := range byID {
		orderedIDs = append(orderedIDs, id)
	}
	slices.Sort(orderedIDs)
	for _, id := range orderedIDs {
		action := canonical.Actions[byID[id]]
		for index, dependency := range action.DependsOn {
			if dependency == "" {
				return Plan[A]{}, invalidPlan(ErrInvalidActionID, "action %q has an empty dependency", id)
			}
			if _, exists := byID[dependency]; !exists {
				return Plan[A]{}, invalidPlan(ErrMissingDependency, "action %q depends on absent action %q", id, dependency)
			}
			if index > 0 && dependency == action.DependsOn[index-1] {
				return Plan[A]{}, invalidPlan(ErrDuplicateDependency, "action %q repeats dependency %q", id, dependency)
			}
		}
	}

	if cycle := findCycle(canonical, byID, orderedIDs); len(cycle) > 0 {
		parts := make([]string, len(cycle))
		for index, id := range cycle {
			parts[index] = formatActionID(id)
		}
		return Plan[A]{}, invalidPlan(ErrPlanCycle, "%s", strings.Join(parts, " -> "))
	}
	return canonical, nil
}

func formatActionID(id ActionID) string {
	value := string(id)
	if utf8.ValidString(value) && !strings.Contains(value, " -> ") {
		safe := true
		for _, character := range value {
			if unicode.IsControl(character) {
				safe = false
				break
			}
		}
		if safe {
			return value
		}
	}
	return strconv.QuoteToASCII(value)
}

func findCycle[A any](plan Plan[A], byID map[ActionID]int, orderedIDs []ActionID) []ActionID {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[ActionID]int, len(orderedIDs))
	stack := make([]ActionID, 0, len(orderedIDs))
	stackIndex := make(map[ActionID]int, len(orderedIDs))
	var visit func(ActionID) []ActionID
	visit = func(id ActionID) []ActionID {
		state[id] = visiting
		stackIndex[id] = len(stack)
		stack = append(stack, id)
		for _, dependency := range plan.Actions[byID[id]].DependsOn {
			switch state[dependency] {
			case unvisited:
				if cycle := visit(dependency); len(cycle) > 0 {
					return cycle
				}
			case visiting:
				start := stackIndex[dependency]
				cycle := append([]ActionID(nil), stack[start:]...)
				cycle = append(cycle, dependency)
				return canonicalCycle(cycle)
			}
		}
		stack = stack[:len(stack)-1]
		delete(stackIndex, id)
		state[id] = visited
		return nil
	}

	for _, id := range orderedIDs {
		if state[id] == unvisited {
			if cycle := visit(id); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

func canonicalCycle(cycle []ActionID) []ActionID {
	if len(cycle) < 2 {
		return cycle
	}
	ring := cycle[:len(cycle)-1]
	minimum := 0
	for index := 1; index < len(ring); index++ {
		if ring[index] < ring[minimum] {
			minimum = index
		}
	}
	canonical := make([]ActionID, 0, len(cycle))
	canonical = append(canonical, ring[minimum:]...)
	canonical = append(canonical, ring[:minimum]...)
	canonical = append(canonical, canonical[0])
	return canonical
}

func insertActionID(ready []ActionID, id ActionID) []ActionID {
	index := sort.Search(len(ready), func(index int) bool {
		return ready[index] >= id
	})
	ready = append(ready, "")
	copy(ready[index+1:], ready[index:])
	ready[index] = id
	return ready
}

func invalidPlan(category error, format string, arguments ...any) error {
	detail := fmt.Sprintf(format, arguments...)
	return fmt.Errorf("%w: %w: %s", ErrInvalidPlan, category, detail)
}
