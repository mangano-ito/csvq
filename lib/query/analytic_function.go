package query

import (
	"sync"

	"github.com/mithrandie/csvq/lib/parser"
	"github.com/mithrandie/csvq/lib/ternary"
	"strings"
)

var AnalyticFunctions map[string]func(*View, parser.AnalyticFunction) error
var defineAnalyticFunctions sync.Once

func DefineAnalyticFunctions() {
	defineAnalyticFunctions.Do(func() {
		AnalyticFunctions = map[string]func(*View, parser.AnalyticFunction) error{
			"ROW_NUMBER":  RowNumber,
			"RANK":        Rank,
			"DENSE_RANK":  DenseRank,
			"FIRST_VALUE": FirstValue,
			"LAST_VALUE":  LastValue,
			"COUNT":       AnalyzeAggregateValue,
			"MIN":         AnalyzeAggregateValue,
			"MAX":         AnalyzeAggregateValue,
			"SUM":         AnalyzeAggregateValue,
			"AVG":         AnalyzeAggregateValue,
			"LISTAGG":     AnalyzeListAgg,
		}
	})
}

type partition interface {
	match([]parser.Primary) bool
	isSameRank([]parser.Primary) bool
}

type partitionBase struct {
	partitionValues []parser.Primary
	orderValues     []parser.Primary
}

func NewPartitionBase(pvalues []parser.Primary, ovalues []parser.Primary) *partitionBase {
	return &partitionBase{
		partitionValues: pvalues,
		orderValues:     ovalues,
	}
}

func (value partitionBase) match(values []parser.Primary) bool {
	for i, v := range value.partitionValues {
		if EquivalentTo(v, values[i]) != ternary.TRUE {
			return false
		}
	}
	return true
}

func (value partitionBase) isSameRank(orderValues []parser.Primary) bool {
	for i, v := range value.orderValues {
		if EquivalentTo(v, orderValues[i]) != ternary.TRUE {
			return false
		}
	}
	return true
}

type partitionList []partition

func (list partitionList) searchIndex(values []parser.Primary) int {
	for idx, v := range list {
		if v.match(values) {
			return idx
		}
	}
	return -1
}

func (list partitionList) replace(idx int, p partition) {
	list[idx] = p
}

func RowNumber(view *View, fn parser.AnalyticFunction) error {
	if fn.Args != nil {
		return NewFunctionArgumentLengthError(fn, fn.Name, []int{0})
	}

	type part struct {
		*partitionBase
		number int64
	}

	var newPart = func(pvalues []parser.Primary) part {
		return part{
			partitionBase: NewPartitionBase(pvalues, nil),
			number:        1,
		}
	}

	var calcNext = func(partition partition) partition {
		p := partition.(part)
		return part{
			partitionBase: p.partitionBase,
			number:        p.number + 1,
		}
	}

	partitions := partitionList{}

	filter := NewFilterForSequentialEvaluation(view, view.ParentFilter)
	for i := range view.Records {
		filter.Records[0].RecordIndex = i
		partitionValues, err := filter.evalValues(fn.AnalyticClause.PartitionValues())
		if err != nil {
			return err
		}

		var idx int
		if idx = partitions.searchIndex(partitionValues); -1 < idx {
			partitions.replace(idx, calcNext(partitions[idx]))
		} else {
			partitions = append(partitions, newPart(partitionValues))
			idx = len(partitions) - 1
		}

		view.Records[i] = append(view.Records[i], NewCell(parser.NewInteger(partitions[idx].(part).number)))
	}

	return nil
}

func analyzeRank(view *View, fn parser.AnalyticFunction, calcFn func(int64, int64, bool) (int64, int64)) error {
	if fn.Args != nil {
		return NewFunctionArgumentLengthError(fn, fn.Name, []int{0})
	}

	type part struct {
		*partitionBase
		number int64
		rank   int64
	}

	var newPart = func(pvalues []parser.Primary, ovalues []parser.Primary) part {
		return part{
			partitionBase: NewPartitionBase(pvalues, ovalues),
			number:        1,
			rank:          1,
		}
	}

	var calcNext = func(partition partition, isSameRank bool) partition {
		p := partition.(part)

		replaceNumber, replaceRank := calcFn(p.number, p.rank, isSameRank)

		return part{
			partitionBase: p.partitionBase,
			number:        replaceNumber,
			rank:          replaceRank,
		}
	}

	partitions := partitionList{}

	filter := NewFilterForSequentialEvaluation(view, view.ParentFilter)
	for i := range view.Records {
		filter.Records[0].RecordIndex = i
		partitionValues, err := filter.evalValues(fn.AnalyticClause.PartitionValues())
		if err != nil {
			return err
		}

		orderValues, err := filter.evalValues(fn.AnalyticClause.OrderValues())
		if err != nil {
			return err
		}

		var idx int
		if idx = partitions.searchIndex(partitionValues); -1 < idx {
			partitions.replace(idx, calcNext(partitions[idx], partitions[idx].isSameRank(orderValues)))
		} else {
			partitions = append(partitions, newPart(partitionValues, orderValues))
			idx = len(partitions) - 1
		}

		view.Records[i] = append(view.Records[i], NewCell(parser.NewInteger(partitions[idx].(part).rank)))
	}

	return nil
}

func Rank(view *View, fn parser.AnalyticFunction) error {
	var nextRank = func(number int64, rank int64, isSameRank bool) (int64, int64) {
		replaceNum := number + 1
		replaceRank := rank
		if !isSameRank {
			replaceRank = replaceNum
		}
		return replaceNum, replaceRank
	}
	return analyzeRank(view, fn, nextRank)
}

func DenseRank(view *View, fn parser.AnalyticFunction) error {
	var nextRank = func(number int64, rank int64, isSameRank bool) (int64, int64) {
		replaceRank := rank
		if !isSameRank {
			replaceRank = replaceRank + 1
		}
		return 0, replaceRank
	}
	return analyzeRank(view, fn, nextRank)
}

func analyzeUniqueValue(view *View, fn parser.AnalyticFunction, compareFn func(parser.Primary, parser.Primary, bool) parser.Primary) error {
	if len(fn.Args) != 1 {
		return NewFunctionArgumentLengthError(fn, fn.Name, []int{1})
	}

	type part struct {
		*partitionBase
		value         parser.Primary
		recordIndices []int
	}

	var newPart = func(pvalues []parser.Primary, value parser.Primary, rowidx int) part {
		return part{
			partitionBase: NewPartitionBase(pvalues, nil),
			value:         value,
			recordIndices: []int{rowidx},
		}
	}

	var calcNext = func(partition partition, value parser.Primary, idx int) partition {
		p := partition.(part)

		replaceValue := compareFn(value, p.value, fn.IgnoreNulls)

		return part{
			partitionBase: p.partitionBase,
			value:         replaceValue,
			recordIndices: append(p.recordIndices, idx),
		}
	}

	partitions := partitionList{}

	filter := NewFilterForSequentialEvaluation(view, view.ParentFilter)
	for i := range view.Records {
		filter.Records[0].RecordIndex = i
		partitionValues, err := filter.evalValues(fn.AnalyticClause.PartitionValues())
		if err != nil {
			return err
		}

		value, err := filter.Evaluate(fn.Args[0])
		if err != nil {
			return err
		}

		if idx := partitions.searchIndex(partitionValues); -1 < idx {
			partitions.replace(idx, calcNext(partitions[idx], value, i))
		} else {
			partitions = append(partitions, newPart(partitionValues, value, i))
		}
	}

	for _, partition := range partitions {
		for _, idx := range partition.(part).recordIndices {
			view.Records[idx] = append(view.Records[idx], NewCell(partition.(part).value))
		}
	}

	return nil
}

func FirstValue(view *View, fn parser.AnalyticFunction) error {
	var compareFn = func(value parser.Primary, current parser.Primary, ignoreNulls bool) parser.Primary {
		if ignoreNulls {
			if parser.IsNull(current) {
				return value
			}
		}
		return current
	}
	return analyzeUniqueValue(view, fn, compareFn)
}

func LastValue(view *View, fn parser.AnalyticFunction) error {
	var compareFn = func(value parser.Primary, current parser.Primary, ignoreNulls bool) parser.Primary {
		if ignoreNulls {
			if parser.IsNull(value) {
				return current
			}
		}
		return value
	}
	return analyzeUniqueValue(view, fn, compareFn)
}

func AnalyzeAggregateValue(view *View, fn parser.AnalyticFunction) error {
	if len(fn.Args) != 1 {
		return NewFunctionArgumentLengthError(fn, fn.Name, []int{1})
	}

	arg := fn.Args[0]
	if _, ok := arg.(parser.AllColumns); ok {
		arg = parser.NewInteger(1)
	}

	type part struct {
		*partitionBase
		values        []parser.Primary
		recordIndices []int
	}

	var newPart = func(pvalues []parser.Primary, value parser.Primary, rowidx int) part {
		return part{
			partitionBase: NewPartitionBase(pvalues, nil),
			values:        []parser.Primary{value},
			recordIndices: []int{rowidx},
		}
	}

	var calcNext = func(partition partition, value parser.Primary, idx int) partition {
		p := partition.(part)

		return part{
			partitionBase: p.partitionBase,
			values:        append(p.values, value),
			recordIndices: append(p.recordIndices, idx),
		}
	}

	partitions := partitionList{}

	filter := NewFilterForSequentialEvaluation(view, view.ParentFilter)
	for i := range view.Records {
		filter.Records[0].RecordIndex = i
		partitionValues, err := filter.evalValues(fn.AnalyticClause.PartitionValues())
		if err != nil {
			return err
		}

		value, err := filter.Evaluate(arg)
		if err != nil {
			return err
		}

		if idx := partitions.searchIndex(partitionValues); -1 < idx {
			partitions.replace(idx, calcNext(partitions[idx], value, i))
		} else {
			partitions = append(partitions, newPart(partitionValues, value, i))
		}
	}

	name := strings.ToUpper(fn.Name)
	aggfunc, _ := AggregateFunctions[name]

	for _, partition := range partitions {
		value := aggfunc(fn.IsDistinct(), partition.(part).values)

		for _, idx := range partition.(part).recordIndices {
			view.Records[idx] = append(view.Records[idx], NewCell(value))
		}
	}

	return nil
}

func AnalyzeListAgg(view *View, fn parser.AnalyticFunction) error {
	if fn.Args == nil || 2 < len(fn.Args) {
		return NewFunctionArgumentLengthError(fn, fn.Name, []int{1, 2})
	}

	type part struct {
		*partitionBase
		values        []parser.Primary
		recordIndices []int
	}

	var newPart = func(pvalues []parser.Primary, value parser.Primary, rowidx int) part {
		return part{
			partitionBase: NewPartitionBase(pvalues, nil),
			values:        []parser.Primary{value},
			recordIndices: []int{rowidx},
		}
	}

	var calcNext = func(partition partition, value parser.Primary, idx int) partition {
		p := partition.(part)

		return part{
			partitionBase: p.partitionBase,
			values:        append(p.values, value),
			recordIndices: append(p.recordIndices, idx),
		}
	}

	partitions := partitionList{}

	filter := NewFilterForSequentialEvaluation(view, view.ParentFilter)
	for i := range view.Records {
		filter.Records[0].RecordIndex = i
		partitionValues, err := filter.evalValues(fn.AnalyticClause.PartitionValues())
		if err != nil {
			return err
		}

		value, err := filter.Evaluate(fn.Args[0])
		if err != nil {
			return err
		}

		if idx := partitions.searchIndex(partitionValues); -1 < idx {
			partitions.replace(idx, calcNext(partitions[idx], value, i))
		} else {
			partitions = append(partitions, newPart(partitionValues, value, i))
		}
	}

	separator := ""
	if len(fn.Args) == 2 {
		p, err := view.ParentFilter.Evaluate(fn.Args[1])
		if err != nil {
			return NewFunctionInvalidArgumentError(fn, fn.Name, "the second argument must be a string")
		}
		s := parser.PrimaryToString(p)
		if parser.IsNull(s) {
			return NewFunctionInvalidArgumentError(fn, fn.Name, "the second argument must be a string")
		}
		separator = s.(parser.String).Value()
	}

	for _, partition := range partitions {
		value := ListAgg(fn.IsDistinct(), partition.(part).values, separator)

		for _, idx := range partition.(part).recordIndices {
			view.Records[idx] = append(view.Records[idx], NewCell(value))
		}
	}

	return nil
}
