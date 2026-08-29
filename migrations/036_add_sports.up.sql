CREATE TABLE venue_sports (
    venue_id BIGINT NOT NULL REFERENCES venues(id) ON DELETE CASCADE,
    sport TEXT NOT NULL CHECK (sport IN ('squash', 'badminton', 'table_tennis', 'tennis', 'padel', 'bowling')),
    courts TEXT NOT NULL DEFAULT '',
    players_per_court INT CHECK (players_per_court IS NULL OR players_per_court >= 1),
    PRIMARY KEY (venue_id, sport)
);

INSERT INTO venue_sports (venue_id, sport, courts)
SELECT id, 'squash', courts FROM venues;

ALTER TABLE venues DROP COLUMN courts;
ALTER TABLE games ADD COLUMN sport TEXT NOT NULL DEFAULT 'squash'
    CHECK (sport IN ('squash', 'badminton', 'table_tennis', 'tennis', 'padel', 'bowling'));
ALTER TABLE games ADD COLUMN players_per_court INT NOT NULL DEFAULT 2 CHECK (players_per_court >= 1);
