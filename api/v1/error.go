package log_v1

type ErrOffsetOutOfRange struct {
	Offset uint64
}

// TODO: implement GRPCStatus()

func (e ErrOffsetOutOfRange) Error() string {
	return ""
}
