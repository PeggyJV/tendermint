package math

type numbers interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

func Max[T numbers](a, b T) T {
	if a > b {
		return a
	}
	return b
}

func MaxInt64(a, b int64) int64 {
	return Max(a, b)
}

func MaxInt(a, b int) int {
	return Max(a, b)
}

//-----------------------------------------------------------------------------

func Min[T numbers](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func MinInt64(a, b int64) int64 {
	return Min(a, b)
}

func MinInt(a, b int) int {
	return Min(a, b)
}
