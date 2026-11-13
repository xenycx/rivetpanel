-- Fill in the app and site names that older audit events left empty.
-- Add-on query events never recorded the app's name; the activity summary
-- grouped them by app and could not read the missing name. Rows whose app
-- or site is gone keep NULL (the summary tolerates it). Data only: no
-- schema change, and running it again changes nothing.
UPDATE audit_events
   SET bot_name = (SELECT substr(name, 1, 64) FROM bots WHERE bots.id = audit_events.bot_id)
 WHERE bot_name IS NULL AND bot_id IS NOT NULL;
UPDATE audit_events
   SET site_name = (SELECT substr(name, 1, 80) FROM sites WHERE sites.id = audit_events.site_id)
 WHERE site_name IS NULL AND site_id IS NOT NULL;
