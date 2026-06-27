const ANNOUNCEMENT_START = '---ANNOUNCEMENT start---';
const ANNOUNCEMENT_END = '---ANNOUNCEMENT end---';
const METADATA_BEGIN = '---METADATA begin---';
const METADATA_END = '---METADATA end---';

export interface Announcement {
    title: string;
    /** UTC Timestamp in milliseconds */
    timestamp: number;
    priority: Priority;
    tags: Tag[];
    /** Markdown content */
    content: string;
}

export enum Priority {
    INFO,
    SUCCESS,
    WARN,
    CRITICAL
}

export interface Tag {
    name: string;
    /** hex color code */
    color: string;
}

const DEFAULT_ANNOUNCEMENT: Announcement = {
    title: '',
    timestamp: 0,
    priority: Priority.INFO,
    tags: [],
    content: ''
};

/**
 * Parses raw announcement data into structured Announcement objects.
 *
 * Expected format:
 * ```
 * ---ANNOUNCEMENT start---
 * ---METADATA begin---
 * Title: Announcement Title
 * Timestamp: 1778939200000
 * Priority: INFO
 * Tags: tag1(#ff0000),tag2(#00ff00)
 * ---METADATA end---
 * This is the announcement content in markdown format.
 * ---ANNOUNCEMENT end---
 * ```
 */
export function parse(raw: string): Announcement[] {
    const announcements: Announcement[] = [];
    const normalized = raw.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
    const lines = normalized.split('\n');

    let currentAnnouncement = { ...DEFAULT_ANNOUNCEMENT };
    let inAnnouncement = false;
    let inMetadata = false;

    for (const line of lines) {
        if (line === ANNOUNCEMENT_START) {
            currentAnnouncement = { ...DEFAULT_ANNOUNCEMENT };
            inAnnouncement = true;
            inMetadata = false;
        } else if (line === ANNOUNCEMENT_END) {
            announcements.push(currentAnnouncement);
            inAnnouncement = false;
            inMetadata = false;
        } else if (line === METADATA_BEGIN) {
            inMetadata = true;
        } else if (line === METADATA_END) {
            inMetadata = false;
        } else if (inAnnouncement && inMetadata) {
            const colonIdx = line.indexOf(':');
            if (colonIdx === -1) continue;
            const key = line.slice(0, colonIdx).trim().toUpperCase();
            const value = line.slice(colonIdx + 1).trim();
            switch (key) {
                case 'TITLE':
                    currentAnnouncement.title = value;
                    break;
                case 'TIMESTAMP':
                    currentAnnouncement.timestamp = parseInt(value, 10);
                    break;
                case 'PRIORITY':
                    currentAnnouncement.priority = Priority[value.toUpperCase() as keyof typeof Priority] ?? Priority.INFO;
                    break;
                case 'TAGS':
                    currentAnnouncement.tags = value.split(',').map(tag => {
                        const [name, color] = tag.split('(#');
                        return {
                            name: name.trim(),
                            color: color ? `#${color.slice(0, -1)}` : ''
                        };
                    });
                    break;
            }
        } else if (inAnnouncement) {
            currentAnnouncement.content += line + '\n';
        }
    }

    return announcements;
}
