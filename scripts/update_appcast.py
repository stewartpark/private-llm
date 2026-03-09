#!/usr/bin/env python3
"""Update appcast.xml with new release information."""

import os
import sys
import xml.etree.ElementTree as ET


def main():
    dmg_size = os.environ.get("DMG_SIZE")
    commit_date = os.environ.get("COMMIT_DATE")
    version = os.environ.get("VERSION")

    if not all([dmg_size, commit_date, version]):
        print("Error: Missing required environment variables")
        print("Required: DMG_SIZE, COMMIT_DATE, VERSION")
        sys.exit(1)

    dmg_size = int(dmg_size)
    version_no_v = version.lstrip("v")
    build_num = "".join(c for c in version_no_v if c.isdigit())

    tree = ET.parse("appcast.xml")
    root = tree.getroot()
    channel = root.find("channel")

    item = ET.SubElement(channel, "item")

    title = ET.SubElement(item, "title")
    title.text = f"Version {version_no_v}"

    link = ET.SubElement(item, "link")
    link.text = f"https://github.com/stewartpark/private-llm/releases/tag/{version}"

    sparkle_version = ET.SubElement(
        item, "{http://www.andymatuschak.org/xml-namespaces/sparkle}version"
    )
    sparkle_version.text = build_num

    short_version = ET.SubElement(
        item, "{http://www.andymatuschak.org/xml-namespaces/sparkle}shortVersionString"
    )
    short_version.text = version_no_v

    pub_date = ET.SubElement(item, "pubDate")
    pub_date.text = commit_date

    enclosure = ET.SubElement(item, "enclosure")
    enclosure.set(
        "url",
        f"https://github.com/stewartpark/private-llm/releases/download/{version}/Private-LLM.dmg",
    )
    enclosure.set("length", str(dmg_size))
    enclosure.set("type", "application/octet-stream")

    min_sys = ET.SubElement(
        item,
        "{http://www.andymatuschak.org/xml-namespaces/sparkle}minimumSystemVersion",
    )
    min_sys.text = "13.0"

    tree.write("appcast.xml", encoding="unicode", xml_declaration=True)


if __name__ == "__main__":
    main()
