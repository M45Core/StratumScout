package model

// ObservationVersion 8 makes matched worker destinations private: only their
// aggregate presence and payout value remain in an observation, while retained
// destination details contain non-worker outputs exclusively.
// Readers remain backward compatible with version 1 latency-only records and
// version 7 destination records.
const ObservationVersion = 8
