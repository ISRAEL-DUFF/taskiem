# Websites that embed the storefront and forms

A business that already has a website can put its storefront's catalogue, a form or an order button on it ([storefronts 6.11](spec/storefronts.md#611-on-a-website-the-business-already-has), [forms 6.9](spec/forms.md#69-on-a-website)). The public host's `/embed/` pages may be framed only by the websites the organisation has **proven it controls**. Every other public page may not be framed at all.

This is built in Phase 5, S1. The embed views, script and overlay come with S6; until then a verified site frames a "Coming soon" page.

## Why sites must be proven

Without proof, anyone could frame a real business's pages:
- A scam site could frame them to look trustworthy.
- A page could put an invisible frame under its own buttons and trick visitors into ordering (clickjacking).

The `frame-ancestors` policy names only proven sites.

## Adding a site

1. **Add it** (Settings, or `POST /v1/embed-sites` with `{"origin": "adakitchen.ng", "method": "dns"}`). The address is normalised to an origin (`https://adakitchen.ng`): https only, no path or port. Refused:
   - IP addresses and punycode names;
   - government and protected names;
   - the platform's own domains (`TASKIEM_PUBLIC_URL`'s host and `TASKIEM_PUBLIC_DOMAINS`).

   An organisation can add up to its plan's `embed_sites`: 10 by default, 1 on Shop and 3 on Shop Plus ([moderation](moderation.md#plans)).
2. **Publish the proof** the answer gives, one of:
   - **DNS:** a TXT record `_taskiem-verify.adakitchen.ng` with the value `taskiem-site-verify=…`.
   - **File:** `https://adakitchen.ng/.well-known/taskiem-site.txt` containing that value on a line of its own. This suits site builders that do not give DNS access. The file is fetched through the egress guard: public addresses and the site's own host only.
3. **Check it** (`POST /v1/embed-sites/{id}/verify`). The answer is 409 until the record or file holds the value; DNS can take a while. Once verified, the public host lets the site frame `/embed/` pages within 30 seconds.

Removing a site (`DELETE /v1/embed-sites/{id}`) stops it within 30 seconds. All of this needs `storefront.manage` (owners and admins), and each step is audited: `embed_site.add`, `embed_site.verify`, `embed_site.remove`.

## Proven again

The scheduler checks every verified site again every 6 hours.
- A site whose record or file no longer holds its value, 4 checks in a row over at least 24 hours, is **unverified**: it stops framing the pages, and `embed_site.unverified` is audited with the reason.
- A check that says nothing (a DNS timeout, a site down for a moment) is not counted.

This stops a domain that lapses or changes hands from framing the business's pages. The owners hear of it on the chat they linked (WhatsApp, else Telegram) and on the alert channels: a `domain_unverified` alert saying which record or file to put back.

## Send to my web designer

`POST /v1/embed-sites/{id}/send-to-designer` with `{"email": "…"}` (`storefront.manage`; no-technical-steps N9) emails whoever looks after the website the code that shows the page (`<iframe src="<the page's address>/embed" …>`) and the proof to publish (the TXT record, or the file and its line), so the owner never handles either. 409 when the deployment sends no email or the page is not published. It is audited as `embed_site.sent_to_designer` with the recipient's domain only. The DNS proof's `prove` also has `record_short`, the record's name as registrars ask for it.

In the web app, **On your website** on the storefront page adds a site, shows the registrar guides and the code, sends it to the designer and checks it ([owners' guide](guide/your-website.md)).

## One order form

A business that sells one product on its own website shows just that order form, not the whole page ([forms that sell](spec/forms-that-sell.md) §5.5). An order form's card under Fronts, and **One order form** under **On your website** once a site is verified, give its code (`GET /v1/storefront/embed?action=<id>`):

- **A frame that grows with the form**: the frame (`https://<page>/embed/<front>/a/<form>`) and a few lines that set its height when the form tells it (`storefront:height`), reading only messages from the page's own address and that frame;
- **A plain frame** of a fixed height, for a website builder that takes no script.

The frame posts to the verified sites only: `storefront:height` with its height as it changes (a package chosen, a size for each item shown), and once an order is sent `storefront:submitted` with the form (`action`, its address) and the order's `reference`, never the buyer's name, number or anything typed. A form of the Forms page that now sells ([forms](forms.md#a-form-that-sells)) keeps its own address: `/embed/f/<id>` sends the frame to the order form's.

A framed order form that **follows up a buyer who left** ([storefronts](storefronts.md#following-up-a-buyer-who-left)) sends its keep from inside the frame to its own address (`/embed/…/a/<form>/keep`), never to the website; the website's address is kept with it only when it is one of the business's verified sites, for the owner's list. The follow-up's link opens the order form at the storefront's own address: a website's page that frames it is not known (browsers send its origin only).

## On the public host

`/embed/` pages are served with `Content-Security-Policy: … frame-ancestors <the verified origins>`, read through `taskiem_public_embed_origins`, the public role's second function, and cached for 30 seconds. They are sent without `X-Frame-Options`, which cannot name origins.

With no verified site, or if the lookup fails, the page keeps `frame-ancestors 'none'` and `X-Frame-Options: DENY`: it fails closed.
