package auth

import "reflect"

type reflectType = reflect.Type

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }
