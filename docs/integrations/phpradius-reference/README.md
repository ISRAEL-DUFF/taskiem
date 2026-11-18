# PHP Radius: the client API, as the vendor publishes it

PHP Radius is billing and RADIUS software for internet providers, hosted by its vendor: an ISP does not run it, it is given an admin dashboard and a **client API** on its own portal address. This folder keeps the vendor's own Postman collection of that API, received on 18 November 2026, for the team building the [ISP area](../../spec/isps.md).

| File | What it is |
| --- | --- |
| [php-radius3-client-api.postman_collection.json](php-radius3-client-api.postman_collection.json) | "PHP Radius3 Client API", the vendor's collection with its descriptions and example answers |

**Scrubbed before it was kept.** The collection as received carried live-looking bearer tokens, demo passwords, payment gateways' test keys and two private test addresses. They are replaced by `{{token}}`, `<token>`, `<password>`, `<paystack public key>`, `<flutterwave public key>` and `https://devs1.phpradius.com`. Nothing else was changed. Do not put the original in the repository.

## What it covers

Everything in it acts as **one subscriber** of one ISP, with that subscriber's bearer token. There is no admin API: no listing subscribers, suspending or reconnecting, or changing a plan from outside.

| Group | Calls |
| --- | --- |
| The ISP | `GET /api/ispdetail` (name, contacts, logo, `phoneAsUsername`, `autoUserRegistration`); `GET /api/userregister` (what sign-up asks for) |
| Signing in | `POST /api/login` (username and password); `POST /api/loginPhone` (phone or username alone, the Quick Recharge way in; [the ISP spec](../../spec/isps.md#5-php-radius) relies on it) |
| Signing up | `POST /api/userregister` (without a code), `PUT /api/userregister/1` (with a code by email or SMS), `POST /api/ticketregister` (with a voucher) |
| The account | `GET /api/dashboard/1`, `GET /api/userDetail`, `GET /api/session`, `PUT /api/traffic/{userId}`, `GET /api/invoice`, `GET /api/receipt`, `GET /api/userledger`, `GET /api/rechargeList` |
| Recharging | `GET /api/recharge/5` (plans, tax and the ISP's gateways); per gateway a request (`POST /api/paystack`, `/flutterwave` and eighteen others) and a verification (`PUT /api/<gateway>/1` with the gateway's whole answer); `PUT /api/voucher/{code}` |
| Complaints | `GET /api/sla`, `POST /api/sla` |
| Passwords | `POST /api/password`, `GET /api/forgotPassword/{username}` |

## Notes for whoever builds on it

- Answers come with status `201` for reads as well as writes, and errors as `{"message": "..."}` with `401` or `422`.
- `loginPhone` answers with `abilities`: what this ISP lets its subscribers do (`recharge`, `voucher`, `sla`, `dashboard`...). A tool for something switched off is not offered.
- A gateway request answers with the **ISP's own public key** and a temporary invoice; the subscriber pays the ISP's gateway account directly, and the verification hands the gateway's answer back. Money never passes through the caller.
- The demo host `devs1.phpradius.com` is the vendor's. Calling it, even with the demo accounts the vendor's examples show, needs the founder's go-ahead.
