ALTER TABLE venues ADD COLUMN courts TEXT NOT NULL DEFAULT '';
UPDATE venues v SET courts = vs.courts
FROM venue_sports vs WHERE vs.venue_id = v.id AND vs.sport = 'squash';
ALTER TABLE games DROP COLUMN players_per_court;
ALTER TABLE games DROP COLUMN sport;
DROP TABLE venue_sports;
