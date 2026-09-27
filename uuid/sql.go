package uuid

import (
	"database/sql/driver"
	"fmt"
)

func (id UUID) Value() (driver.Value, error) { return id.String(), nil }

func (id *UUID) Scan(value any) error {
	if id == nil {
		return errorsNewNilTarget()
	}
	var parsed UUID
	var err error
	switch value := value.(type) {
	case string:
		parsed, err = Parse(value)
	case []byte:
		if len(value) == 16 {
			copy(parsed[:], value)
			if parsed.Version() != 7 || parsed.Variant() != 2 {
				err = ErrInvalid
			}
		} else {
			parsed, err = Parse(string(value))
		}
	default:
		err = fmt.Errorf("cannot scan UUIDv7 from %T", value)
	}
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

func errorsNewNilTarget() error { return fmt.Errorf("cannot scan UUIDv7 into nil target") }
