#!/usr/bin/env bats

setup() {
    source ./disk-explorer.sh
}

@test "is_integer: positive integer" {
    run is_integer "123"
    [ "$status" -eq 0 ]
}

@test "is_integer: zero" {
    run is_integer "0"
    [ "$status" -eq 0 ]
}

@test "is_integer: negative integer" {
    run is_integer "-456"
    [ "$status" -eq 0 ]
}

@test "is_integer: non-integer string" {
    run is_integer "abc"
    [ "$status" -eq 1 ]
}

@test "is_integer: decimal number" {
    run is_integer "12.34"
    [ "$status" -eq 1 ]
}

@test "is_integer: empty string" {
    run is_integer ""
    [ "$status" -eq 1 ]
}

@test "is_integer: spaces" {
    run is_integer " 123 "
    [ "$status" -eq 1 ]
}

@test "is_non_negative_int: positive integer" {
    run is_non_negative_int "123"
    [ "$status" -eq 0 ]
}

@test "is_non_negative_int: zero" {
    run is_non_negative_int "0"
    [ "$status" -eq 0 ]
}

@test "is_non_negative_int: negative integer" {
    run is_non_negative_int "-123"
    [ "$status" -eq 1 ]
}
