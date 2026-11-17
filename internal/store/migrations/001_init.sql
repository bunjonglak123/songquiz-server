CREATE TABLE games (
  id           uuid PRIMARY KEY,
  room_code    text        NOT NULL,
  source_kind  text        NOT NULL DEFAULT '',
  rounds       int         NOT NULL,
  player_count int         NOT NULL,
  started_at   timestamptz NOT NULL,
  ended_at     timestamptz NOT NULL
);

CREATE TABLE game_players (
  game_id   uuid NOT NULL REFERENCES games(id) ON DELETE CASCADE,
  player_id text NOT NULL,
  name      text NOT NULL,
  score     int  NOT NULL,
  rank      int  NOT NULL,
  correct   int  NOT NULL,
  PRIMARY KEY (game_id, player_id)
);

CREATE INDEX game_players_score_idx ON game_players (score DESC);
CREATE INDEX games_ended_at_idx ON games (ended_at);
