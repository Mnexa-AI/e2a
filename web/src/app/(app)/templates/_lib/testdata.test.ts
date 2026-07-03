import { flattenSuggested, nestTestData } from "./testdata";

describe("flattenSuggested", () => {
  it("passes flat keys through unchanged", () => {
    expect(flattenSuggested({ name: "name_value", total: "total_value" })).toEqual({
      name: "name_value",
      total: "total_value",
    });
  });

  it("flattens nested objects into dotted keys", () => {
    expect(
      flattenSuggested({
        user: { name: "user.name_value", contact: { email: "user.contact.email_value" } },
        plain: "plain_value",
      }),
    ).toEqual({
      "user.name": "user.name_value",
      "user.contact.email": "user.contact.email_value",
      plain: "plain_value",
    });
  });

  it("returns an empty map for empty input", () => {
    expect(flattenSuggested({})).toEqual({});
  });
});

describe("nestTestData", () => {
  it("keeps undotted keys flat", () => {
    expect(nestTestData({ name: "Zoe" })).toEqual({ name: "Zoe" });
  });

  it("nests dotted keys into objects", () => {
    expect(
      nestTestData({ "user.name": "Zoe", "user.contact.email": "z@x.com", plain: "p" }),
    ).toEqual({
      user: { name: "Zoe", contact: { email: "z@x.com" } },
      plain: "p",
    });
  });

  it("first writer wins on scalar/object conflicts", () => {
    // "user" is claimed as a scalar first; "user.name" cannot nest under it.
    expect(nestTestData({ user: "scalar", "user.name": "Zoe" })).toEqual({
      user: "scalar",
    });
  });
});

describe("round-trip", () => {
  it("flatten(nested) then nest() reproduces the nested wire shape", () => {
    const nested = {
      user: { name: "user.name_value", contact: { email: "user.contact.email_value" } },
      order_id: "order_id_value",
    };
    expect(nestTestData(flattenSuggested(nested))).toEqual(nested);
  });
});
