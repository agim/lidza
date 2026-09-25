package llm

import "reflect"

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }
